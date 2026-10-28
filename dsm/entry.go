package dsm

// The entry gate decides who may use the app at all, before any route policy.
// It wraps the session authenticator, so every API method, transfer and the
// window's own calls share one decision; apps never implement it themselves.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

const (
	// EntryAdminOnly admits only DSM administrators.
	EntryAdminOnly = "admin-only"
	// EntryAppPrivilege admits administrators and any account that DSM's
	// Control Panel > Application Privileges allows for this app.
	EntryAppPrivilege = "app-privilege"

	// EntryCacheTTL bounds how long a privilege change can take to apply.
	EntryCacheTTL = 30 * time.Second
	// EntryCacheMaxEntries bounds the cache file whatever the traffic.
	EntryCacheMaxEntries = 256

	entryCacheFile = "entry-cache.json"
)

var packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{1,63}$`)

// EntryGate is an api.Authenticator that admits a verified caller only when
// the app's entry mode allows it.
type EntryGate struct {
	next    api.Authenticator
	mode    string
	app     string
	dir     string
	now     func() time.Time
	allowed func(ctx context.Context, principal, app, addr string) (bool, error)
}

// EntryMode keeps App admission visible to api.Server, including public routes.
func (g *EntryGate) EntryMode() string { return g.mode }

// NewEntryGate wraps next for the build-time entry mode. An empty mode keeps
// next unchanged: the route policy alone decides, as before entry modes.
// dataDir holds the short-lived decision cache shared by CGI processes.
func NewEntryGate(next api.Authenticator, mode, packageID, dataDir string) (api.Authenticator, error) {
	if next == nil {
		return nil, errors.New("entry gate needs an authenticator")
	}
	switch mode {
	case "":
		return next, nil
	case EntryAdminOnly, EntryAppPrivilege:
	default:
		return nil, fmt.Errorf("unknown entry mode %q", mode)
	}
	if !packageName.MatchString(packageID) {
		return nil, fmt.Errorf("invalid package ID %q", packageID)
	}
	return &EntryGate{next: next, mode: mode, app: "SYNO.SDS." + packageID + ".Instance", dir: filepath.Join(dataDir, "entry"), now: time.Now, allowed: AppPrivilege}, nil
}

func (g *EntryGate) Authenticate(r *http.Request) (api.Identity, error) {
	id, err := g.next.Authenticate(r)
	if err != nil || id.Admin {
		return id, err
	}
	if g.mode == EntryAdminOnly {
		return api.Identity{}, api.ErrAdminRequired
	}
	addr, err := api.ClientAddress(r)
	if err != nil {
		return api.Identity{}, err
	}
	allowed, err := g.decide(r.Context(), id.User, addr)
	if err != nil {
		return api.Identity{}, err
	}
	if !allowed {
		return api.Identity{}, api.ErrAppPrivilegeDenied
	}
	return id, nil
}

type cachedDecision struct {
	Allowed bool  `json:"allowed"`
	At      int64 `json:"at"`
}

func (g *EntryGate) decide(ctx context.Context, principal, addr string) (bool, error) {
	sum := sha256.Sum256([]byte(principal + "\x00" + g.app + "\x00" + addr))
	key := hex.EncodeToString(sum[:])
	now := g.now().Unix()
	entries, _ := g.readCache()
	if d, ok := entries[key]; ok && d.At <= now && now-d.At < int64(EntryCacheTTL/time.Second) {
		return d.Allowed, nil
	}
	allowed, err := g.allowed(ctx, principal, g.app, addr)
	if err != nil {
		return false, err
	}
	if entries == nil {
		entries = map[string]cachedDecision{}
	}
	entries[key] = cachedDecision{Allowed: allowed, At: now}
	g.writeCache(entries, now)
	return allowed, nil
}

func (g *EntryGate) readCache() (map[string]cachedDecision, error) {
	data, err := os.ReadFile(filepath.Join(g.dir, entryCacheFile))
	if err != nil {
		return nil, err
	}
	var entries map[string]cachedDecision
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// writeCache keeps only fresh entries, newest first, up to the bound. A failed
// write only costs the next request a fresh lookup.
func (g *EntryGate) writeCache(entries map[string]cachedDecision, now int64) {
	keys := make([]string, 0, len(entries))
	for k, d := range entries {
		if d.At <= now && now-d.At < int64(EntryCacheTTL/time.Second) {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return entries[keys[i]].At > entries[keys[j]].At })
	if len(keys) > EntryCacheMaxEntries {
		keys = keys[:EntryCacheMaxEntries]
	}
	kept := make(map[string]cachedDecision, len(keys))
	for _, k := range keys {
		kept[k] = entries[k]
	}
	data, err := json.Marshal(kept)
	if err != nil || os.MkdirAll(g.dir, 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(g.dir, entryCacheFile+".*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil || tmp.Close() != nil {
		tmp.Close()
		return
	}
	os.Rename(tmp.Name(), filepath.Join(g.dir, entryCacheFile))
}
