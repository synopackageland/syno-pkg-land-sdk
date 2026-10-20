package idgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// OptionalPassthroughRule is a fixed, default-off app-authenticated path set.
// Its enable state is private package data, never a manifest default.
type OptionalPassthroughRule struct {
	Prefixes []string `json:"prefixes"`
}

func pathPrefix(path, prefix string) bool {
	base := strings.TrimSuffix(prefix, "/")
	return path == base || strings.HasPrefix(path, base+"/")
}

func validateOptional(rules map[string]OptionalPassthroughRule, permanent []string) error {
	if len(rules) > 16 {
		return errors.New("at most 16 optional passthrough entries")
	}
	prefixes := append([]string(nil), permanent...)
	for id, rule := range rules {
		if !namePattern.MatchString(id) || len(rule.Prefixes) == 0 || len(rule.Prefixes) > 16 {
			return errors.New("optional passthrough needs a valid id and 1-16 fixed prefixes")
		}
		for _, p := range rule.Prefixes {
			if !strings.HasPrefix(p, "/") || p == "/" || len(p) > 512 || strings.Contains(p, "//") || strings.ContainsAny(p, "%?#\\\x00\r\n\t ") || !cleanPath(p) || pathPrefix(p, "/_sso") {
				return fmt.Errorf("invalid optional passthrough prefix %q", p)
			}
			for _, other := range prefixes {
				if pathPrefix(p, other) || pathPrefix(other, p) {
					return errors.New("optional passthrough prefixes must not overlap each other or permanent passthrough")
				}
			}
			prefixes = append(prefixes, p)
		}
	}
	return nil
}

// Reject aliases before any app-authenticated prefix comparison. A second
// decoding must not turn a literal percent escape into another path segment.
func cleanOptionalPath(r *http.Request) bool {
	for _, p := range []string{r.URL.Path, r.URL.EscapedPath()} {
		if !cleanPath(p) || strings.Contains(p, "//") {
			return false
		}
	}
	escaped := strings.ToLower(r.URL.EscapedPath())
	if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") {
		return false
	}
	// URL.Path is already decoded once. Reject nested encodings of path
	// delimiters/dot segments, while preserving ordinary literal percent names.
	decoded := strings.ToLower(r.URL.Path)
	return !strings.Contains(decoded, "%2e") && !strings.Contains(decoded, "%2f") && !strings.Contains(decoded, "%5c") && !strings.Contains(decoded, "%25")
}

type optionalPassthrough struct {
	dir     string
	rules   map[string]OptionalPassthroughRule
	mu      sync.RWMutex
	enabled map[string]bool
	ctx     context.Context
	cancel  context.CancelFunc
}

func optionalStatePath(dir, id string) string { return filepath.Join(dir, "passthrough-"+id+".json") }

func readOptionalState(dir, id string) bool {
	path := optionalStatePath(dir, id)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16 {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var enabled bool
	return json.Unmarshal(raw, &enabled) == nil && enabled
}

func (p *optionalPassthrough) reload() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.enabled = make(map[string]bool, len(p.rules))
	for id := range p.rules {
		p.enabled[id] = readOptionalState(p.dir, id)
	}
}

// ReloadOptionalPassthrough cancels active optional requests and reloads only
// package-owned state. A missing or invalid state file disables that entry.
func (f *Front) ReloadOptionalPassthrough() {
	if f.optional != nil {
		f.optional.reload()
	}
}

func (p *optionalPassthrough) admit(path string) (string, context.Context, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for id, rule := range p.rules {
		for _, prefix := range rule.Prefixes {
			if pathPrefix(path, prefix) {
				// The disk check closes new admissions immediately after atomic disable,
				// even before SIGHUP is scheduled. Enabling still requires a reload.
				return id, p.ctx, p.enabled[id] && readOptionalState(p.dir, id)
			}
		}
	}
	return "", nil, false
}

func (p *optionalPassthrough) destinationAllowed(r *http.Request, id string) bool {
	raw := r.Header.Get("Destination")
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.IsAbs() && u.Scheme+"://"+u.Host != requestOrigin(r) {
		return false
	}
	if u.Host != "" && !u.IsAbs() {
		return false
	}
	target := &http.Request{URL: u}
	if !cleanOptionalPath(target) {
		return false
	}
	for _, prefix := range p.rules[id].Prefixes {
		if pathPrefix(u.Path, prefix) {
			return true
		}
	}
	return false
}

// writeOptionalState uses one private file per fixed id. Independent entries
// cannot lose each other's updates and no cross-process merge lock is needed.
func writeOptionalState(dir, id string, enabled bool) error {
	raw, _ := json.Marshal(enabled)
	return replaceOptionalFile(dir, "passthrough-"+id+".json", append(raw, '\n'))
}

func replaceOptionalFile(dir, name string, raw []byte) error {
	if !filepath.IsAbs(dir) {
		return errors.New("gateway state directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("gateway state directory must be private")
	}
	f, err := os.CreateTemp(dir, ".passthrough-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// OptionalPassthroughRoutes exposes admin-only SDK settings operations. Apps
// register these routes on their existing authenticated CGI/API; no new port,
// control socket, root helper or gateway admin endpoint is installed.
func (c Config) OptionalPassthroughRoutes() []api.Route {
	status := func() map[string]bool {
		out := map[string]bool{}
		for id := range c.OptionalPassthrough {
			out[id] = readOptionalState(c.StateDir, id)
		}
		return out
	}
	return []api.Route{
		api.Handle("gateway.passthrough.status", func(_ context.Context, caller api.Caller, params json.RawMessage) (any, error) {
			if err := api.RequireAdmin(caller); err != nil {
				return nil, err
			}
			if err := validateOptional(c.OptionalPassthrough, c.Passthrough); err != nil {
				return nil, err
			}
			if len(params) != 0 && string(params) != "{}" && string(params) != "null" {
				return nil, errors.New("status accepts no parameters")
			}
			return map[string]any{"configured_enabled": status()}, nil
		}),
		api.Handle("gateway.passthrough.set", func(_ context.Context, caller api.Caller, params json.RawMessage) (any, error) {
			if err := api.RequireAdmin(caller); err != nil {
				return nil, err
			}
			if err := validateOptional(c.OptionalPassthrough, c.Passthrough); err != nil {
				return nil, err
			}
			var req struct {
				ID      string `json:"id"`
				Enabled *bool  `json:"enabled"`
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(params, &fields) != nil || len(fields) != 2 || fields["id"] == nil || fields["enabled"] == nil || json.Unmarshal(params, &req) != nil || req.Enabled == nil {
				return nil, errors.New("set requires only id and enabled")
			}
			if _, ok := c.OptionalPassthrough[req.ID]; !ok {
				return nil, errors.New("optional passthrough id is not declared")
			}
			if err := writeOptionalState(c.StateDir, req.ID, *req.Enabled); err != nil {
				return nil, err
			}
			if err := notifyOptionalGateway(c.StateDir); err != nil {
				// Never leave an enabled route after a failed reload notification.
				rollback := writeOptionalState(c.StateDir, req.ID, false)
				return nil, errors.Join(err, rollback)
			}
			return map[string]any{"configured_enabled": status(), "reload_requested": true}, nil
		}),
	}
}
