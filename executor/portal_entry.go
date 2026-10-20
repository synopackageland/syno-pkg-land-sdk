package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// The package name becomes DSM file names (app.d/<pkg>.json and
// <c>.syno-app-portal.<pkg>.conf): no separators, no empty or dot-only parts.
var portalPackageRE = regexp.MustCompile(`^[A-Za-z0-9]+(?:[._-][A-Za-z0-9]+)*$`)

// One URL path segment, as the portal package reads DSM's saved alias.
var portalAliasRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func validPortal(pkg string, p PortalPolicy) bool {
	return len(pkg) <= 64 && portalPackageRE.MatchString(pkg) && portalAliasRE.MatchString(p.Alias) &&
		p.Application != "" && utf8.ValidString(p.Application) && utf8.RuneCountInString(p.Application) <= 64 && !strings.ContainsFunc(p.Application, unicode.IsControl)
}

// Entry states that status reports for a policy with a portal entry.
const (
	PortalRegistered      = "registered"
	PortalUnregistered    = "unregistered"
	PortalCleanupRequired = "cleanup-required"
)

// PortalEntry registers the policy's entry in DSM's Application Portal the
// way DSM's web-config worker does, and removes it again.
type PortalEntry interface {
	Register(context.Context) error
	Unregister(context.Context) error
	Entry() string
}

// portalPaths are DSM's Application Portal list, nginx's enabled conf.d and
// site links, the per-package settings and synow3tool's conf registry.
type portalPaths struct{ AppD, ConfD, Sites, Settings, Registry string }

var dsmPortal = portalPaths{AppD: "/usr/syno/etc/www/app.d", ConfD: "/usr/local/etc/nginx/conf.d", Sites: "/usr/local/etc/nginx/sites-enabled", Settings: "/usr/syno/etc/packages", Registry: "/usr/syno/etc/www/nginx_config.json"}

// portalMustache renders the app through DSM's own CGI; nothing in it comes
// from the package.
const portalMustache = "{{> /usr/syno/share/nginx/cgi}}\n"

// DSM names the confs of app <pkg> <c>.syno-app-portal.<pkg>.conf.
var portalConfs = []string{"dsm.", "www.", "alias.", ".alias."}
var portalSites = []string{"server.", ".server."}

// portalStepTimeout bounds the whole register or unregister; a reply that
// comes too late for the caller still finishes the steps.
const portalStepTimeout = 2 * time.Minute

type portalEntry struct {
	Package string
	Policy  PortalPolicy
	Paths   portalPaths
	DSM     *liveDSM
	// StateDir is the protected folder of the cleanup-required marker and
	// the lock a running change holds.
	StateDir        string
	OperationBudget time.Duration
}

type registryEntry struct {
	Name    string `json:"real-name"`
	Sites   bool   `json:"sites"`
	Enabled bool   `json:"enable"`
}

func (p portalEntry) appID() string { return "SYNO.SDS." + p.Package + ".Instance" }

// appJSON is the app.d file in the format DSM 7.2's worker copies; DSM 7.1
// reads it too. Its alias is a default DSM does not apply.
func (p portalEntry) appJSON() []byte {
	b, _ := json.Marshal(struct {
		Application   string    `json:"application"`
		ID            string    `json:"id"`
		Alias         string    `json:"alias"`
		FQDN          *string   `json:"fqdn"`
		AlternatePort struct{}  `json:"alternatePort"`
		XAccel        bool      `json:"x-accel"`
		Include       []string  `json:"include"`
		Injectable    bool      `json:"injectable"`
		SSL           portalSSL `json:"ssl"`
		Index         string    `json:"index"`
		Root          string    `json:"root"`
		Version       int       `json:"version"`
	}{Application: p.Policy.Application, ID: p.appID(), Alias: p.Policy.Alias, Include: []string{}, Index: "index.cgi", Root: "/usr/syno/synoman", Version: 2})
	return b
}

type portalSSL struct {
	SPDY bool `json:"spdy"`
	HSTS bool `json:"hsts"`
}

func (p portalEntry) file(ext string) string { return filepath.Join(p.Paths.AppD, p.Package+ext) }
func (p portalEntry) marker() string         { return filepath.Join(p.StateDir, "portal-cleanup") }
func (p portalEntry) lock() string           { return filepath.Join(p.StateDir, "portal-change") }

// begin waits for any other change, then marks the entry. The change holds
// the lock until it releases the returned file, after its marker is gone.
func (p portalEntry) begin(ctx context.Context) (*os.File, error) {
	lock, err := os.OpenFile(p.lock(), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = flock(lock, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			err = writeMarker(p.marker())
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			break
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(50 * time.Millisecond):
			continue
		}
		break
	}
	if err != nil {
		release(lock)
		return nil, err
	}
	return lock, nil
}

// marked reads a marker that a running change holds as registered: the
// change ends with its result. A marker nobody holds is what a
// failed change left. It answers "" without a marker.
func (p portalEntry) marked() string {
	if absent(p.marker()) {
		return ""
	}
	lock, err := os.OpenFile(p.lock(), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return PortalCleanupRequired
	}
	defer release(lock)
	if flock(lock, syscall.LOCK_SH|syscall.LOCK_NB) != nil {
		return PortalRegistered
	}
	// With the lock held, no change can have written the marker now.
	if absent(p.marker()) {
		return ""
	}
	return PortalCleanupRequired
}

// Register writes the mustache before the json, which makes the app appear
// in DSM's list, has DSM generate its conf and checks nginx and the list. A
// failure removes everything again; the entry stays cleanup-required when
// that removal fails too.
func (p portalEntry) Register(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, p.operationBudget())
	defer cancel()
	lock, err := p.begin(ctx)
	if err != nil {
		return err
	}
	defer release(lock)
	err = p.register(ctx)
	if err == nil {
		return removeMarker(p.marker())
	}
	// A registration may have consumed its whole deadline. Cleanup must
	// still be able to run; it has its own bound and retains the marker on failure.
	cleanup, done := context.WithTimeout(context.WithoutCancel(parent), min(5*time.Second, p.operationBudget()/4))
	defer done()
	if rollback := p.unregister(cleanup); rollback == nil {
		removeMarker(p.marker())
	} else {
		log.Printf("portal rollback failed: package=%s %s", p.Package, portalFailure(rollback))
	}
	log.Printf("portal register failed: package=%s %s", p.Package, portalFailure(err))
	return err
}

func (p portalEntry) register(ctx context.Context) error {
	if err := writePublic(p.file(".mustache"), []byte(portalMustache)); err != nil {
		return portalStep("write-mustache", err)
	}
	if err := writePublic(p.file(".json"), p.appJSON()); err != nil {
		return portalStep("write-json", err)
	}
	if err := p.run(ctx, "/usr/syno/bin/synow3tool", "--gen-app="+p.Package); err != nil {
		return err
	}
	if err := p.run(ctx, "/usr/bin/nginx", "-t"); err != nil {
		return err
	}
	return portalStep("list", p.listed(ctx))
}

func (p portalEntry) listed(ctx context.Context) error {
	data, err := p.DSM.call(ctx, "SYNO.Core.AppPortal", "list", "2", "", nil)
	if err != nil {
		return err
	}
	var out struct {
		Portal []struct {
			ID string `json:"id"`
		} `json:"portal"`
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return err
	}
	for _, app := range out.Portal {
		if app.ID == p.appID() {
			return nil
		}
	}
	return errors.New("portal not listed")
}

// Unregister removes the app's files and every conf DSM generated for it,
// enabled or only registered, then reloads nginx once it accepts the
// result. Each step that finds nothing to do is skipped, so a retry
// finishes what a failed attempt left.
func (p portalEntry) Unregister(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, p.operationBudget())
	defer cancel()
	lock, err := p.begin(ctx)
	if err != nil {
		return err
	}
	defer release(lock)
	if err := p.unregister(ctx); err != nil {
		return err
	}
	return removeMarker(p.marker())
}

func (p portalEntry) unregister(ctx context.Context) error {
	var failed error
	for _, f := range []string{p.file(".json"), p.file(".mustache"), filepath.Join(p.Paths.Settings, p.Package, "AppPortal.json")} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			failed = err
		}
	}
	registry, err := readRegistry(p.Paths.Registry)
	if err != nil {
		failed = err
	}
	for _, kind := range []struct {
		prefixes []string
		dir      string
		flag     string
		sites    bool
	}{{portalConfs, p.Paths.ConfD, "confd", false}, {portalSites, p.Paths.Sites, "sites", true}} {
		for _, prefix := range kind.prefixes {
			name := prefix + "syno-app-portal." + p.Package + ".conf"
			_, err := os.Lstat(filepath.Join(kind.dir, name))
			enabled := err == nil
			if enabled {
				if err = p.run(ctx, "/usr/syno/bin/synow3tool", "--disable-nginx-"+kind.flag+"="+name); err != nil {
					failed = err
					continue
				}
			}
			if enabled || registered(registry, name, kind.sites) {
				if err = p.run(ctx, "/usr/syno/bin/synow3tool", "--unreg-nginx-"+kind.flag+"="+name); err != nil {
					failed = err
				}
			}
		}
	}
	// nginx keeps running its old configuration when the new one is refused.
	if err := p.run(ctx, "/usr/bin/nginx", "-t"); err != nil {
		return err
	}
	if err := p.run(ctx, "/usr/syno/bin/synow3tool", "--nginx=reload"); err != nil {
		return err
	}
	return failed
}

// Entry reads the entry from DSM's files: registered needs exactly the
// executor's two app.d files and the conf DSM generated from them.
func (p portalEntry) Entry() string {
	if entry := p.marked(); entry != "" {
		return entry
	}
	app, appErr := os.ReadFile(p.file(".json"))
	mustache, mustacheErr := os.ReadFile(p.file(".mustache"))
	registry, registryErr := readRegistry(p.Paths.Registry)
	if registryErr != nil {
		return PortalCleanupRequired
	}
	confs := 0
	for _, prefix := range append(append([]string{}, portalConfs...), portalSites...) {
		name := prefix + "syno-app-portal." + p.Package + ".conf"
		_, confErr := os.Lstat(filepath.Join(p.Paths.ConfD, name))
		_, siteErr := os.Lstat(filepath.Join(p.Paths.Sites, name))
		if confErr == nil || siteErr == nil || registered(registry, name, false) || registered(registry, name, true) {
			confs++
		}
	}
	if os.IsNotExist(appErr) && os.IsNotExist(mustacheErr) && confs == 0 {
		return PortalUnregistered
	}
	_, dsmErr := os.Lstat(filepath.Join(p.Paths.ConfD, "dsm.syno-app-portal."+p.Package+".conf"))
	if appErr == nil && mustacheErr == nil && bytes.Equal(app, p.appJSON()) && string(mustache) == portalMustache && dsmErr == nil {
		return PortalRegistered
	}
	return PortalCleanupRequired
}

func (p portalEntry) run(ctx context.Context, path string, args ...string) error {
	_, err := p.DSM.Command.Exec(ctx, path, args)
	return portalStep(filepath.Base(path)+" "+strings.Split(args[0], "=")[0], err)
}

// readRegistry reads synow3tool's conf registry, which only synow3tool
// writes; a missing registry has no entries.
func readRegistry(path string) ([]registryEntry, error) {
	f, err := openRegular(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := readLimited(f, 1<<20)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Configs []registryEntry `json:"configs"`
	}
	if err = json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	return doc.Configs, nil
}

func registered(registry []registryEntry, name string, sites bool) bool {
	for _, e := range registry {
		if e.Name == name && e.Sites == sites {
			return true
		}
	}
	return false
}

// writePublic replaces path with a 0644 file DSM's web server can read.
func writePublic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0644); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func writeMarker(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func absent(path string) bool {
	_, err := os.Lstat(path)
	return os.IsNotExist(err)
}

func flock(f *os.File, how int) error {
	for {
		if err := syscall.Flock(int(f.Fd()), how); err != syscall.EINTR {
			return err
		}
	}
}

// release unlocks before closing. Closing alone would leave the lock held
// while a child forked meanwhile keeps the descriptor until exec, and Entry
// would read the ended change as running.
func release(lock *os.File) {
	flock(lock, syscall.LOCK_UN)
	lock.Close()
}

func removeMarker(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Journal only fixed step names and bounded reason codes, never command
// output, request credentials or DSM error text.
type portalStepError struct {
	step  string
	cause error
}

func (e *portalStepError) Error() string { return "portal " + e.step + ": " + failureReason(e.cause) }
func (e *portalStepError) Unwrap() error { return e.cause }
func portalStep(step string, err error) error {
	if err == nil {
		return nil
	}
	return &portalStepError{step, err}
}
func portalFailure(err error) string {
	step := "state"
	var e *portalStepError
	if errors.As(err, &e) {
		step = e.step
	}
	return fmt.Sprintf("step=%s reason=%s", step, failureReason(err))
}

func (p portalEntry) operationBudget() time.Duration {
	if p.OperationBudget > 0 {
		return p.OperationBudget
	}
	return portalStepTimeout
}
