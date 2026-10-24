package idgateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/externalorigin"
)

const DefaultProjection = "/var/packages/SynoPkgConnect/var/spl/external.json"
const FallbackProjection = "/var/packages/SynoPkgConnect/target/spl/external.json"
const maxExternalRevision uint64 = 9999999999999999999

// ExternalConfig is the optional gateway.json external policy.
// Omission defaults to optional Connect support and no origin-change prompt.
type ExternalConfig struct {
	Connect      string `json:"connect,omitempty"`
	OriginChange string `json:"origin_change,omitempty"`
}

func (e *ExternalConfig) validate() error {
	if e == nil {
		return nil
	}
	if e.Connect != "" && e.Connect != "optional" && e.Connect != "none" {
		return errors.New("external.connect must be optional or none")
	}
	if e.OriginChange != "" && e.OriginChange != "none" && e.OriginChange != "confirm" {
		return errors.New("external.origin_change must be none or confirm")
	}
	return nil
}

// ExternalState is the App-owned state_file. Only settings.cgi writes it.
type ExternalState struct {
	Schema       int    `json:"schema"`
	Revision     uint64 `json:"revision"`
	ManualOrigin string `json:"manual_origin"`
	DSMOrigin    string `json:"dsm_origin"`
}

// ExternalProjection is Connect's public, read-only projection (schema 1).
type ExternalProjection struct {
	Schema int                              `json:"schema"`
	DSM    ExternalProjectionDSM            `json:"dsm"`
	Apps   map[string]ExternalProjectionApp `json:"apps"`
}
type ExternalProjectionDSM struct {
	Origin string `json:"origin"`
}
type ExternalProjectionApp struct {
	Port   int                      `json:"port"`
	Rev    uint64                   `json:"rev"`
	Public ExternalProjectionPublic `json:"public"`
}
type ExternalProjectionPublic struct {
	Origin string `json:"origin"`
}

// External contains one consistent resolution of both inputs. Revisions are
// durable counters, never hashes; Accept is ordered manual then Connect.
type External struct {
	Accept  []string
	Primary string
	DSM     string
	Manual  string
	Connect string
	R1, R2  uint64
}

func (e External) Index(origin string) int {
	canonical, err := externalorigin.Canonical(origin)
	if err != nil {
		return -1
	}
	return slices.Index(e.Accept, canonical)
}

// ExternalStatePath is fixed under the package's private gateway state directory.
func (c Config) ExternalStatePath() string { return filepath.Join(c.StateDir, "external.json") }

func optionalExternalOrigin(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	canonical, err := externalorigin.Canonical(raw)
	return canonical, err == nil
}
func (s ExternalState) valid() bool {
	_, a := optionalExternalOrigin(s.ManualOrigin)
	_, b := optionalExternalOrigin(s.DSMOrigin)
	return s.Schema == 1 && s.Revision <= maxExternalRevision && a && b
}
func (p ExternalProjection) valid() bool {
	if p.Schema != 1 || len(p.Apps) > 4096 {
		return false
	}
	if _, ok := optionalExternalOrigin(p.DSM.Origin); !ok {
		return false
	}
	for pkg, app := range p.Apps {
		if !namePattern.MatchString(pkg) || !validPort(app.Port, false) || app.Rev > maxExternalRevision {
			return false
		}
		if _, ok := optionalExternalOrigin(app.Public.Origin); !ok {
			return false
		}
	}
	return true
}

// EffectiveExternal is the shared, side-effect-free resolver for gateway,
// settings.cgi and Compose. Malformed inputs count as absent independently.
func EffectiveExternal(cfg Config, state ExternalState, projection ExternalProjection) External {
	var out External
	if cfg.External.validate() != nil {
		return out
	}
	if state.valid() {
		out.R1 = state.Revision
		out.Manual, _ = optionalExternalOrigin(state.ManualOrigin)
		out.DSM, _ = optionalExternalOrigin(state.DSMOrigin)
	}
	if (cfg.External == nil || cfg.External.Connect != "none") && projection.valid() {
		if app, ok := projection.Apps[cfg.Package]; ok && app.Port == nativePublicPort(cfg) {
			out.Connect, _ = optionalExternalOrigin(app.Public.Origin)
			out.R2 = app.Rev
		}
		if out.DSM == "" {
			out.DSM, _ = optionalExternalOrigin(projection.DSM.Origin)
		}
	}
	// Revalidate origin separation whenever either input changes.
	if out.Manual == out.DSM {
		out.Manual = ""
	}
	if out.Connect == out.DSM {
		out.Connect = ""
	}
	for _, origin := range []string{out.Manual, out.Connect} {
		if origin != "" && !slices.Contains(out.Accept, origin) {
			out.Accept = append(out.Accept, origin)
		}
	}
	out.Primary = cfg.PublicOrigin
	if len(out.Accept) != 0 {
		out.Primary = out.Accept[0]
	}
	return out
}

var errExternalFile = errors.New("invalid external access file")

// readExternalFile refuses link, ownership, mode and replacement races. DSM's
// package var/target directory links are resolved by OpenRoot; the file itself
// and its immediate parent must be real, protected filesystem objects.
func readExternalFile(path string, private bool, v any) (os.FileInfo, error) {
	parent := filepath.Dir(path)
	dir, err := os.Lstat(parent)
	if err != nil {
		return nil, err
	}
	owner, ok := dir.Sys().(*syscall.Stat_t)
	if !dir.IsDir() || dir.Mode().Perm()&0022 != 0 || !ok || private && owner.Uid != uint32(os.Geteuid()) {
		return nil, errExternalFile
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	mode, limit := os.FileMode(0644), int64(1<<20)
	if private {
		mode, limit = 0600, 4096
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode || !ok || stat.Uid != owner.Uid || stat.Nlink != 1 || info.Size() < 1 || info.Size() > limit {
		return nil, errExternalFile
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || info.Mode() != opened.Mode() || info.Size() != opened.Size() || !info.ModTime().Equal(opened.ModTime()) {
		return nil, errExternalFile
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errExternalFile
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, errExternalFile
	}
	return opened, nil
}

// LoadExternalState performs a fresh validated read. A missing file is an empty
// schema-1 state with revision zero. Other errors never expose partial state.
func LoadExternalState(cfg Config) (ExternalState, error) {
	state := ExternalState{Schema: 1}
	_, err := readExternalFile(cfg.ExternalStatePath(), true, &state)
	if errors.Is(err, os.ErrNotExist) {
		return ExternalState{Schema: 1}, nil
	}
	if err != nil || !state.valid() {
		return ExternalState{}, errExternalFile
	}
	return state, nil
}

// ExternalReader polls stat at most once per five seconds. It never performs
// network I/O or writes state; invalid input is absent and logged at INFO at
// most once per hour, without including any input content.
type ExternalReader struct {
	mu              sync.Mutex
	cfg             Config
	paths           []string
	now             func() time.Time
	infof           func(string, ...any)
	checked, logged time.Time
	selected        string
	files           map[string]os.FileInfo
	state           ExternalState
	projection      ExternalProjection
}

func NewExternalReader(cfg Config) *ExternalReader {
	return &ExternalReader{cfg: cfg, paths: []string{DefaultProjection, FallbackProjection}, now: time.Now, infof: log.Printf, files: map[string]os.FileInfo{}}
}
func (r *ExternalReader) invalid(now time.Time) {
	if r.logged.IsZero() || now.Sub(r.logged) >= time.Hour {
		r.infof("INFO external access input is invalid; treating it as absent")
		r.logged = now
	}
}
func (r *ExternalReader) changedFile(path string) bool {
	info, err := os.Lstat(path)
	previous := r.files[path]
	if err != nil {
		delete(r.files, path)
		return true
	}
	r.files[path] = info
	return previous == nil || !os.SameFile(previous, info) || !info.ModTime().Equal(previous.ModTime()) || info.Mode() != previous.Mode() || info.Size() != previous.Size() || externalOwnerChanged(previous, info)
}
func externalOwnerChanged(a, b os.FileInfo) bool {
	x, ok := a.Sys().(*syscall.Stat_t)
	y, good := b.Sys().(*syscall.Stat_t)
	return !ok || !good || x.Uid != y.Uid || x.Gid != y.Gid || x.Nlink != y.Nlink
}
func (r *ExternalReader) changed(path string) bool {
	parent := r.changedFile(filepath.Dir(path))
	file := r.changedFile(path)
	return parent || file
}
func (r *ExternalReader) Read() External {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.checked.IsZero() || now.Sub(r.checked) >= 5*time.Second {
		r.checked = now
		path := r.cfg.ExternalStatePath()
		if r.changed(path) {
			state, err := LoadExternalState(r.cfg)
			if err != nil {
				r.invalid(now)
			}
			r.state = state
		}
		if r.cfg.External == nil || r.cfg.External.Connect != "none" {
			// Select only a readable location. A malformed first projection must not
			// revive a stale fallback projection.
			for _, path := range r.paths {
				_, statErr := os.Lstat(path)
				if errors.Is(statErr, os.ErrNotExist) || errors.Is(statErr, os.ErrPermission) {
					continue
				}
				changed := r.changed(path)
				if changed || r.selected != path {
					r.selected = path
					var p ExternalProjection
					_, err := readExternalFile(path, false, &p)
					if err != nil || !p.valid() {
						p = ExternalProjection{}
						r.invalid(now)
					}
					r.projection = p
				}
				return EffectiveExternal(r.cfg, r.state, r.projection)
			}
			r.projection = ExternalProjection{}
			r.selected = ""
			// Forget prior observations, so restoring the same inode reloads it.
			for _, path := range r.paths {
				delete(r.files, path)
			}
		}
	}
	return EffectiveExternal(r.cfg, r.state, r.projection)
}

func (e External) Revision() string { return fmt.Sprintf("%d.%d", e.R1, e.R2) }
