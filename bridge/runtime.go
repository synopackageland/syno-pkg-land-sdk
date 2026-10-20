//go:build unix

package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"github.com/synopackageland/syno-pkg-land-sdk/delegate"
	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
	"github.com/synopackageland/syno-pkg-land-sdk/filestation"
	"github.com/synopackageland/syno-pkg-land-sdk/identity"
	"github.com/synopackageland/syno-pkg-land-sdk/portal"
)

// PermissionsFile is the App's method policy in the App directory.
const PermissionsFile = "permissions.json"

// Config sets up the runtime. bridge/cmd/runtime fills it from the values
// the package build binds; tests override the paths.
type Config struct {
	PackageID string
	// PortalExecutor is bound by the build for portal Apps targeting DSM 7.1.
	PortalExecutor bool
	// AppDir holds bridge.json, permissions.json and the App;
	// empty is <PackagesDir>/<package>/target/app.
	AppDir string
	// DataDir is the package's data directory; empty is <PackagesDir>/<package>/var.
	DataDir string
	// ManifestDigest and PermissionsDigest are the SHA-256 (hex) of the files
	// the build validated. Empty skips the check: development builds only.
	ManifestDigest, PermissionsDigest string
	// Auth verifies the DSM session; nil is dsm.NewAuthenticator().
	Auth api.Authenticator
	// ScriptName is the CGI's SCRIPT_NAME; the identity capability derives
	// its cookie path from it.
	ScriptName string
	// PackagesDir is /var/packages unless set; Python is /usr/bin/python3 unless set.
	PackagesDir, Python string
	// StartTimeout bounds the wait for the App's hello; 0 is DefaultStartTimeout.
	StartTimeout time.Duration
	// ExecutorSocket is the package executor's socket; empty is
	// /run/synopkgland/<package>/executor.sock. Only Go tests set it, to serve a
	// fake executor (delegate/delegatetest) without root: the package CGI
	// (bridge/cmd/runtime) never does, so no request can choose the socket.
	ExecutorSocket string
}

var packageID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// NewRuntime is the package CGI handler for an App in another language
// (spec/bridge/v1): it reads the App directory, then checks every request
// as a Go App's CGI does before it hands the call to the App.
func NewRuntime(cfg Config) (http.Handler, error) {
	if !packageID.MatchString(cfg.PackageID) {
		return nil, fmt.Errorf("bridge: invalid package ID %q", cfg.PackageID)
	}
	cfg = cfg.withDefaults()
	m, err := cfg.manifest()
	if err != nil {
		return nil, err
	}
	permissions, err := readBound(filepath.Join(cfg.AppDir, PermissionsFile), cfg.PermissionsDigest, api.MaxPermissionsBytes)
	if err != nil {
		return nil, err
	}
	sv := cfg.services(m)
	if cfg.StartTimeout <= 0 {
		seconds := m.TimeoutSeconds
		if seconds <= 0 {
			seconds = 60
		}
		cfg.StartTimeout = time.Duration(seconds) * time.Second / 2
	}
	session := &Session{Dial: cfg.exec(m), Methods: m.Methods, StartTimeout: cfg.StartTimeout, Services: sv.table(),
		Transfers: m.Transfers, Spool: filepath.Join(cfg.DataDir, SpoolDir), MaxUploadBytes: m.MaxUploadBytes}
	routes := append(Routes(session), sv.routes()...)
	auth := cfg.Auth
	var standalone *identity.Service
	if m.Capabilities.Identity == "standalone" {
		if standalone, err = cfg.identity(); err != nil {
			return nil, err
		}
		auth = standalone
	}
	if auth, err = dsm.NewEntryGate(auth, m.Access, cfg.PackageID, cfg.DataDir); err != nil {
		return nil, err
	}
	srv, err := api.NewServerWithPermissions(auth, permissions, routes...)
	if err != nil {
		return nil, err
	}
	if m.MaxRequestBytes > 0 {
		if err := srv.SetMaxRequestBytes(m.MaxRequestBytes); err != nil {
			return nil, err
		}
	}
	var h http.Handler = srv
	if standalone != nil {
		h = standalone.Wrap(h)
	}
	var deadline []api.DeadlineOption
	if m.TimeoutSeconds > 0 {
		deadline = append(deadline, api.Timeout(time.Duration(m.TimeoutSeconds)*time.Second))
	}
	descriptionAuth, err := dsm.NewEntryGate(cfg.Auth, m.Access, cfg.PackageID, cfg.DataDir)
	if err != nil {
		return nil, err
	}
	h = api.WithDescription(h, descriptionAuth, filepath.Join(cfg.PackagesDir, cfg.PackageID, "target", api.DescriptionFile))
	return api.WithRequestDeadline(h, deadline...), nil
}

// manifest reads the bridge.json the build validated.
func (cfg Config) manifest() (Manifest, error) {
	raw, err := readBound(filepath.Join(cfg.AppDir, ManifestFile), cfg.ManifestDigest, MaxManifestBytes)
	if err != nil {
		return Manifest{}, err
	}
	return ParseManifest(raw)
}

func (cfg Config) withDefaults() Config {
	if cfg.PackagesDir == "" {
		cfg.PackagesDir = "/var/packages"
	}
	if cfg.AppDir == "" {
		cfg.AppDir = filepath.Join(cfg.PackagesDir, cfg.PackageID, "target", "app")
	}
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(cfg.PackagesDir, cfg.PackageID, "var")
	}
	if cfg.Python == "" {
		cfg.Python = "/usr/bin/python3"
	}
	if cfg.Auth == nil {
		cfg.Auth = dsm.NewAuthenticator()
	}
	return cfg
}

// readBound reads a file the build validated, refusing other contents.
func readBound(name, digest string, limit int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("bridge: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("bridge: %w", err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("bridge: %s is larger than %d bytes", name, limit)
	}
	if digest != "" && fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
		return nil, fmt.Errorf("bridge: %s does not match the package build", name)
	}
	return raw, nil
}

// exec starts the App as spec/bridge/v1 (App manifest) describes.
func (cfg Config) exec(m Manifest) Dialer {
	entry := filepath.Join(cfg.AppDir, filepath.FromSlash(m.Entry))
	e := &Exec{Path: entry, Dir: cfg.AppDir, Env: cfg.env(m)}
	switch m.Runtime {
	case "python":
		e.Path, e.Args = cfg.Python, []string{entry}
		return pythonExec{e}
	case "node":
		e.Path, e.Args = filepath.Join(cfg.PackagesDir, m.Node.Package, "target", "usr", "local", "bin", "node"), []string{entry}
	}
	return e
}

// env is the App's whole environment: the manifest's, then the runtime's,
// which wins over it (os/exec keeps the last value of a name).
func (cfg Config) env(m Manifest) []string {
	var env []string
	for name, value := range m.Env {
		env = append(env, name+"="+value)
	}
	slices.Sort(env)
	env = append(env,
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"SYNOPKGLAND_PACKAGE="+cfg.PackageID,
		"SYNOPKGLAND_DATA_DIR="+cfg.DataDir)
	if m.Runtime == "python" {
		env = append(env, "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1", "PYTHONNOUSERSITE=1")
	}
	return env
}

// services are the capabilities the manifest asks for, each on the
// package's own data directory and executor.
func (cfg Config) services(m Manifest) services {
	caps := m.Capabilities
	sv := services{dataDir: cfg.DataDir, maxSpool: m.MaxUploadBytes, lookupUID: delegate.LookupUID}
	if sv.maxSpool == 0 {
		sv.maxSpool = MaxUploadBytes
	}
	if caps.Files == "session" {
		sv.files = filestation.Local(cfg.DataDir)
	}
	if caps.Portal {
		app := "SYNO.SDS." + cfg.PackageID + ".Instance"
		sv.portal = func() portal.Status { return portal.Read(portal.ConfigPath(cfg.PackageID), app) }
		if cfg.PortalExecutor {
			sv.portalExecutor = cfg.executor()
		}
	}
	if caps.Jobs || caps.Delegate != nil {
		sv.jobs = background.NewRequestStore(cfg.jobDir())
	}
	if caps.Delegate != nil {
		sv.delegate = cfg.delegate(caps.Delegate, sv.jobs)
	}
	if !caps.Jobs {
		sv.jobs = nil
	}
	if caps.Root || caps.WebAPI {
		sv.webAPIOnly = !caps.Root
		sv.executor = cfg.executor()
		sv.webAPI = caps.WebAPI
		sv.executorRoutes = delegate.ExecutorRoutes(cfg.executor(), cfg.DataDir)
	}
	return sv
}

// executor is the client of the package's executor, on ExecutorSocket when
// set; it reads the lifecycle mailbox in the data directory.
func (cfg Config) executor() *delegate.Client {
	c := delegate.Local(cfg.PackageID)
	c.Mailbox = filepath.Join(cfg.DataDir, "executor")
	if cfg.ExecutorSocket != "" {
		c.Socket = cfg.ExecutorSocket
	}
	return c
}

// jobDir is the package's job store in the data directory.
func (cfg Config) jobDir() string { return filepath.Join(cfg.DataDir, "background") }

// delegate is the package's executor integration on store; d nil leaves only
// the store, for a worker that runs plain jobs.
func (cfg Config) delegate(d *DelegateCapability, store *background.Store) *delegate.Service {
	s := &delegate.Service{Client: cfg.executor(), Store: store, StagingDir: filepath.Join(cfg.DataDir, "executor-staging")}
	if d != nil {
		s.ExecutorDigest, s.PolicyDigest, s.Scope, s.Destinations = d.ExecutorDigest, d.PolicyDigest, d.Scope, d.Destinations
		s.Operations, s.Lifetime = []string{"files.moveFromStaging"}, "until-revoked"
		s.InstallDraftPath, s.SessionFiles = filepath.Join(cfg.DataDir, "delegate-install.json"), filestation.Local(cfg.DataDir)
	}
	return s
}

// routes are the capabilities' browser methods.
func (sv services) routes() []api.Route {
	routes := append(sv.files.Routes(), sv.executorRoutes...)
	if sv.delegate != nil {
		routes = append(routes, sv.delegate.Routes()...)
	}
	if sv.portal != nil {
		routes = append(routes, api.Handle("portal.status", sv.portalStatus))
		if sv.portalExecutor != nil {
			routes = append(routes, api.HandleRequest("portal.register", sv.portalChange(portal.Register)), api.HandleRequest("portal.unregister", sv.portalChange(portal.Unregister)))
		}
	}
	return routes
}

func noParams(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&struct{}{}) != nil {
		return api.BadRequest("this method takes no params")
	}
	return nil
}

// identity is the SDK's App sign-in; its cookie covers the package's UI,
// two levels above the CGI (<ui>/cgi/api.cgi).
func (cfg Config) identity() (*identity.Service, error) {
	name := cfg.ScriptName
	if !strings.HasPrefix(name, "/") || strings.HasPrefix(name, "//") || path.Clean(name) != name || !strings.HasSuffix(name, "/cgi/api.cgi") {
		return nil, errors.New("bridge: identity needs the CGI's absolute SCRIPT_NAME ending in /cgi/api.cgi")
	}
	return identity.New(identity.Config{DataDir: cfg.DataDir, CookiePath: strings.TrimSuffix(path.Dir(path.Dir(name)), "/") + "/"})
}
