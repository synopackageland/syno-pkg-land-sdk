package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// portalDSM plays synow3tool, nginx and the AppPortal list on a temporary
// tree laid out like DSM's: --gen-app registers and enables the app's conf.d
// file, disable removes the enabled link, unreg drops the registry entry.
type portalDSM struct {
	paths portalPaths
	pkg   string
	calls []string
	// fail names the calls that exit non-zero, such as "nginx -t".
	fail map[string]bool
	// unlisted keeps the app out of SYNO.Core.AppPortal list.
	unlisted bool
}

func (d *portalDSM) Exec(_ context.Context, path string, args []string) ([]byte, error) {
	call := filepath.Base(path) + " " + strings.Join(args, " ")
	if path == "/usr/syno/bin/synowebapi" {
		call = "list"
		if !slices.Equal(args, []string{"--exec", "api=SYNO.Core.AppPortal", "method=list", "version=2"}) {
			return nil, errors.New("unexpected webapi call")
		}
	}
	d.calls = append(d.calls, call)
	if d.fail[call] {
		return nil, errors.New("exit status 1")
	}
	switch {
	case path == "/usr/syno/bin/synowebapi":
		portal := []map[string]any{{"id": "SYNO.SDS.App.FileStation3.Instance"}}
		if _, err := os.Stat(filepath.Join(d.paths.AppD, d.pkg+".json")); err == nil && !d.unlisted {
			portal = append(portal, map[string]any{"id": "SYNO.SDS." + d.pkg + ".Instance"})
		}
		return json.Marshal(map[string]any{"success": true, "data": map[string]any{"portal": portal}})
	case path == "/usr/bin/nginx" && slices.Equal(args, []string{"-t"}):
		return nil, nil
	case path != "/usr/syno/bin/synow3tool" || len(args) != 1:
		return nil, errors.New("unexpected command " + call)
	}
	flag, value, _ := strings.Cut(args[0], "=")
	switch flag {
	case "--gen-app":
		if value != d.pkg {
			return nil, errors.New("another app")
		}
		if _, err := os.Stat(filepath.Join(d.paths.AppD, d.pkg+".json")); err != nil {
			return []byte("may not exist"), nil
		}
		d.register("dsm.syno-app-portal."+d.pkg+".conf", false)
		return []byte("Update config success\n"), nil
	case "--nginx":
		if value != "reload" {
			return nil, errors.New("unexpected nginx action")
		}
		return nil, nil
	case "--disable-nginx-confd", "--disable-nginx-sites":
		return nil, os.Remove(filepath.Join(d.dir(flag), value))
	case "--unreg-nginx-confd", "--unreg-nginx-sites":
		entries := d.registry()
		kept := entries[:0]
		for _, e := range entries {
			if e.Name != value {
				kept = append(kept, e)
			}
		}
		d.save(kept)
		return nil, nil
	}
	return nil, errors.New("unexpected synow3tool flag " + flag)
}

func (d *portalDSM) dir(flag string) string {
	if strings.HasSuffix(flag, "-sites") {
		return d.paths.Sites
	}
	return d.paths.ConfD
}

// register is what DSM itself does for a conf: a registry entry and, while
// enabled, a link in conf.d or sites-enabled.
func (d *portalDSM) register(name string, sites bool) {
	dir := d.paths.ConfD
	if sites {
		dir = d.paths.Sites
	}
	if err := os.Symlink("../conf.d-available/"+name+".w3conf", filepath.Join(dir, name)); err != nil && !os.IsExist(err) {
		panic(err)
	}
	entries := slices.DeleteFunc(d.registry(), func(e registryEntry) bool { return e.Name == name })
	d.save(append(entries, registryEntry{Name: name, Sites: sites, Enabled: true}))
}

func (d *portalDSM) registry() []registryEntry {
	b, err := os.ReadFile(d.paths.Registry)
	if os.IsNotExist(err) {
		return nil
	}
	var doc struct {
		Configs []registryEntry `json:"configs"`
	}
	if err != nil || json.Unmarshal(b, &doc) != nil {
		panic("registry")
	}
	return doc.Configs
}

func (d *portalDSM) save(entries []registryEntry) {
	b, _ := json.Marshal(map[string]any{"configs": entries, "version": 1})
	if err := os.WriteFile(d.paths.Registry, b, 0644); err != nil {
		panic(err)
	}
}

// portalFixture lays out app.d, conf.d, sites-enabled, the packages
// settings folder and the protected state folder.
func portalFixture(t *testing.T, pkg string) (*portalDSM, portalEntry) {
	t.Helper()
	// The registry is opened without symlinks; macOS links its temp folder.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	paths := portalPaths{AppD: filepath.Join(root, "app.d"), ConfD: filepath.Join(root, "conf.d"), Sites: filepath.Join(root, "sites-enabled"), Settings: filepath.Join(root, "packages"), Registry: filepath.Join(root, "nginx_config.json")}
	for _, dir := range []string{paths.AppD, paths.ConfD, paths.Sites, filepath.Join(paths.Settings, pkg), filepath.Join(root, "protected")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	d := &portalDSM{paths: paths, pkg: pkg, fail: map[string]bool{}}
	return d, portalEntry{Package: pkg, Policy: PortalPolicy{Application: "Demo App", Alias: "demo"}, Paths: paths, DSM: &liveDSM{Command: d}, StateDir: filepath.Join(root, "protected")}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPortalRegisterWritesTheAppAndGeneratesItsConf(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	if err := p.Register(context.Background()); err != nil {
		t.Fatal(err, d.calls)
	}
	want := `{"application":"Demo App","id":"SYNO.SDS.Demo.Instance","alias":"demo","fqdn":null,"alternatePort":{},"x-accel":false,"include":[],"injectable":false,"ssl":{"spdy":false,"hsts":false},"index":"index.cgi","root":"/usr/syno/synoman","version":2}`
	if got := readFile(t, filepath.Join(d.paths.AppD, "Demo.json")); got != want {
		t.Fatal(got)
	}
	if got := readFile(t, filepath.Join(d.paths.AppD, "Demo.mustache")); got != "{{> /usr/syno/share/nginx/cgi}}\n" {
		t.Fatal(got)
	}
	for _, name := range []string{"Demo.json", "Demo.mustache"} {
		info, err := os.Stat(filepath.Join(d.paths.AppD, name))
		if err != nil || info.Mode().Perm() != 0644 {
			t.Fatal(name, info.Mode(), err)
		}
	}
	if !slices.Equal(d.calls, []string{"synow3tool --gen-app=Demo", "nginx -t", "list"}) {
		t.Fatal(d.calls)
	}
	if got := p.Entry(); got != PortalRegistered {
		t.Fatal(got)
	}
}

func TestPortalRegisterAgainKeepsTheEntry(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	for i := 0; i < 2; i++ {
		if err := p.Register(context.Background()); err != nil {
			t.Fatal(i, err, d.calls)
		}
	}
	if got := p.Entry(); got != PortalRegistered {
		t.Fatal(got)
	}
	if n := len(d.registry()); n != 1 {
		t.Fatal("registry entries", n)
	}
}

func TestPortalUnregisterRemovesEveryFileAndConfOfTheApp(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	if err := p.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	// What an administrator's alias, fqdn and port add through AppPortal set.
	for _, c := range []string{"alias.", ".alias.", "www."} {
		d.register(c+"syno-app-portal.Demo.conf", false)
	}
	for _, c := range []string{"server.", ".server."} {
		d.register(c+"syno-app-portal.Demo.conf", true)
	}
	settings := filepath.Join(d.paths.Settings, "Demo", "AppPortal.json")
	if err := os.WriteFile(settings, []byte(`{"id":"SYNO.SDS.Demo.Instance","alias":"demo"}`), 0644); err != nil {
		t.Fatal(err)
	}
	// Another app's files stay.
	d.register("dsm.syno-app-portal.FileStation.conf", false)
	os.WriteFile(filepath.Join(d.paths.AppD, "FileStation.json"), []byte(`{}`), 0644)
	d.calls = nil
	if err := p.Unregister(context.Background()); err != nil {
		t.Fatal(err, d.calls)
	}
	for _, f := range []string{filepath.Join(d.paths.AppD, "Demo.json"), filepath.Join(d.paths.AppD, "Demo.mustache"), settings} {
		if _, err := os.Lstat(f); !os.IsNotExist(err) {
			t.Fatal("left", f)
		}
	}
	want := []string{
		"synow3tool --disable-nginx-confd=dsm.syno-app-portal.Demo.conf", "synow3tool --unreg-nginx-confd=dsm.syno-app-portal.Demo.conf",
		"synow3tool --disable-nginx-confd=www.syno-app-portal.Demo.conf", "synow3tool --unreg-nginx-confd=www.syno-app-portal.Demo.conf",
		"synow3tool --disable-nginx-confd=alias.syno-app-portal.Demo.conf", "synow3tool --unreg-nginx-confd=alias.syno-app-portal.Demo.conf",
		"synow3tool --disable-nginx-confd=.alias.syno-app-portal.Demo.conf", "synow3tool --unreg-nginx-confd=.alias.syno-app-portal.Demo.conf",
		"synow3tool --disable-nginx-sites=server.syno-app-portal.Demo.conf", "synow3tool --unreg-nginx-sites=server.syno-app-portal.Demo.conf",
		"synow3tool --disable-nginx-sites=.server.syno-app-portal.Demo.conf", "synow3tool --unreg-nginx-sites=.server.syno-app-portal.Demo.conf",
		"nginx -t", "synow3tool --nginx=reload",
	}
	if !slices.Equal(d.calls, want) {
		t.Fatal(d.calls)
	}
	if r := d.registry(); len(r) != 1 || r[0].Name != "dsm.syno-app-portal.FileStation.conf" {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(d.paths.AppD, "FileStation.json")); err != nil {
		t.Fatal("removed another app", err)
	}
	if got := p.Entry(); got != PortalUnregistered {
		t.Fatal(got)
	}
}

func TestPortalUnregisterOfNothingOnlyReloads(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	if err := p.Unregister(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.calls, []string{"nginx -t", "synow3tool --nginx=reload"}) {
		t.Fatal(d.calls)
	}
	if got := p.Entry(); got != PortalUnregistered {
		t.Fatal(got)
	}
}

// A disabled conf still in DSM's registry is unregistered on retry.
func TestPortalUnregisterFinishesADisabledConf(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	d.save([]registryEntry{{Name: "alias.syno-app-portal.Demo.conf"}})
	if err := p.Unregister(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.calls, []string{"synow3tool --unreg-nginx-confd=alias.syno-app-portal.Demo.conf", "nginx -t", "synow3tool --nginx=reload"}) {
		t.Fatal(d.calls)
	}
}

func TestPortalRegisterFailureRollsBack(t *testing.T) {
	for _, failing := range []string{"synow3tool --gen-app=Demo", "nginx -t", "list"} {
		d, p := portalFixture(t, "Demo")
		if failing == "list" {
			d.unlisted = true
		} else {
			d.fail[failing] = true
		}
		if p.Register(context.Background()) == nil {
			t.Fatal(failing, "succeeded")
		}
		for _, f := range []string{"Demo.json", "Demo.mustache"} {
			if _, err := os.Lstat(filepath.Join(d.paths.AppD, f)); !os.IsNotExist(err) {
				t.Fatal(failing, "left", f)
			}
		}
		if len(d.registry()) != 0 {
			t.Fatal(failing, "left a conf", d.registry())
		}
		// nginx -t still failing keeps the old configuration running.
		want := PortalUnregistered
		if failing == "nginx -t" {
			want = PortalCleanupRequired
			if slices.Contains(d.calls, "synow3tool --nginx=reload") {
				t.Fatal("reloaded a configuration nginx refused", d.calls)
			}
		} else if d.calls[len(d.calls)-1] != "synow3tool --nginx=reload" {
			t.Fatal(failing, d.calls)
		}
		if got := p.Entry(); got != want {
			t.Fatal(failing, got)
		}
	}
}

func TestPortalCleanupRequiredUntilAnUnregisterSucceeds(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	if err := p.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.fail["synow3tool --unreg-nginx-confd=dsm.syno-app-portal.Demo.conf"] = true
	if p.Unregister(context.Background()) == nil {
		t.Fatal("unregister succeeded")
	}
	if got := p.Entry(); got != PortalCleanupRequired {
		t.Fatal(got)
	}
	d.fail = map[string]bool{}
	if err := p.Unregister(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := p.Entry(); got != PortalUnregistered {
		t.Fatal(got)
	}
}

func TestPortalEntryReadsPartialFilesAsCleanupRequired(t *testing.T) {
	for name, prepare := range map[string]func(*portalDSM){
		"json only": func(d *portalDSM) { os.WriteFile(filepath.Join(d.paths.AppD, "Demo.json"), []byte(`{}`), 0644) },
		"mustache only": func(d *portalDSM) {
			os.WriteFile(filepath.Join(d.paths.AppD, "Demo.mustache"), []byte(portalMustache), 0644)
		},
		"conf only": func(d *portalDSM) { d.register("alias.syno-app-portal.Demo.conf", false) },
		"unregistered conf": func(d *portalDSM) {
			d.save([]registryEntry{{Name: "dsm.syno-app-portal.Demo.conf"}})
		},
	} {
		d, p := portalFixture(t, "Demo")
		prepare(d)
		if got := p.Entry(); got != PortalCleanupRequired {
			t.Fatal(name, got)
		}
	}
}

// portalWatch reads the entry as another reader sees it at every DSM
// command of a running change, then runs the command.
type portalWatch struct {
	twin portalEntry
	dsm  *portalDSM
	seen []string
}

func (w *portalWatch) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	w.seen = append(w.seen, w.twin.Entry())
	return w.dsm.Exec(ctx, path, args)
}

// a change that is still running is not a leftover of a failed one.
func TestPortalEntryReadsARunningChangeAsRegistered(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	w := &portalWatch{twin: p, dsm: d}
	p.DSM = &liveDSM{Command: w}
	if err := p.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Unregister(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.seen) == 0 {
		t.Fatal("no command ran")
	}
	for _, got := range w.seen {
		if got != PortalRegistered {
			t.Fatal(w.seen)
		}
	}
	if got := p.Entry(); got != PortalUnregistered {
		t.Fatal(got)
	}
}

func TestPortalEntryReadsAMarkerNobodyHoldsAsCleanupRequired(t *testing.T) {
	_, p := portalFixture(t, "Demo")
	os.WriteFile(filepath.Join(p.StateDir, "portal-cleanup"), nil, 0600)
	if got := p.Entry(); got != PortalCleanupRequired {
		t.Fatal(got)
	}
	os.WriteFile(filepath.Join(p.StateDir, "portal-change"), nil, 0600)
	if got := p.Entry(); got != PortalCleanupRequired {
		t.Fatal(got)
	}
}

// portalFork hands, at the first DSM command of a change, the change's lock
// descriptor to a child that keeps it until killed, as a thread spawning a
// command shares it between fork and exec.
type portalFork struct {
	t     *testing.T
	lock  string
	dsm   *portalDSM
	child *exec.Cmd
}

func (f *portalFork) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	if f.child == nil {
		held := heldDescriptor(f.t, f.lock)
		defer held.Close()
		f.child = exec.Command("sleep", "60")
		f.child.ExtraFiles = []*os.File{held}
		if err := f.child.Start(); err != nil {
			f.t.Fatal(err)
		}
		f.t.Cleanup(func() { f.child.Process.Kill(); f.child.Wait() })
	}
	return f.dsm.Exec(ctx, path, args)
}

// heldDescriptor duplicates this process's open descriptor of path.
func heldDescriptor(t *testing.T, path string) *os.File {
	var want syscall.Stat_t
	if err := syscall.Stat(path, &want); err != nil {
		t.Fatal(err)
	}
	for fd := 3; fd < 1024; fd++ {
		var st syscall.Stat_t
		if syscall.Fstat(fd, &st) != nil || st.Dev != want.Dev || st.Ino != want.Ino {
			continue
		}
		syscall.ForkLock.RLock()
		dup, err := syscall.Dup(fd)
		if err == nil {
			syscall.CloseOnExec(dup)
		}
		syscall.ForkLock.RUnlock()
		if err != nil {
			t.Fatal(err)
		}
		return os.NewFile(uintptr(dup), path)
	}
	t.Fatal("no open descriptor of", path)
	return nil
}

// a child that shares the change's lock does not make a change that
// ended read as running.
func TestPortalEntryReadsAFailedChangeAsCleanupRequiredWhileAForkLives(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	d.fail["nginx -t"] = true
	f := &portalFork{t: t, lock: p.lock(), dsm: d}
	p.DSM = &liveDSM{Command: f}
	if p.Register(context.Background()) == nil {
		t.Fatal("register succeeded")
	}
	if f.child == nil {
		t.Fatal("no command ran")
	}
	if got := p.Entry(); got != PortalCleanupRequired {
		t.Fatal(got)
	}
}

// A file someone else put at the app's name is never adopted as registered.
func TestPortalEntryDoesNotTrustAChangedFile(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	if err := p.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(d.paths.AppD, "Demo.mustache"), []byte("include /tmp/x;\n"), 0644)
	if got := p.Entry(); got != PortalCleanupRequired {
		t.Fatal(got)
	}
}

// A timed-out register must get a fresh bounded context for rollback. Reusing
// the registration deadline prevents DSM commands from removing partial state.
type portalRollbackDSM struct {
	*portalDSM
	firstDeadline time.Time
}

func (d *portalRollbackDSM) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	deadline, _ := ctx.Deadline()
	if len(args) == 1 && args[0] == "--gen-app=Demo" {
		d.firstDeadline = deadline
		_, _ = d.portalDSM.Exec(ctx, path, args)
		<-ctx.Done()
		return nil, context.DeadlineExceeded
	}
	if !d.firstDeadline.IsZero() && !deadline.After(d.firstDeadline) {
		return nil, context.DeadlineExceeded
	}
	return d.portalDSM.Exec(ctx, path, args)
}
func TestPortalRegisterTimeoutStillRollsBack(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	p.DSM = &liveDSM{Command: &portalRollbackDSM{portalDSM: d}}
	p.OperationBudget = 100 * time.Millisecond
	if !errors.Is(p.Register(context.Background()), context.DeadlineExceeded) {
		t.Fatal("expected registration timeout")
	}
	if got := p.Entry(); got != PortalUnregistered {
		t.Fatal("rollback left partial state", got, d.calls)
	}
	if len(d.registry()) != 0 {
		t.Fatal("registered conf survived", d.registry())
	}
}

func TestPortalDiagnosticsNeverIncludeCommandOutputOrErrorText(t *testing.T) {
	secret := errors.New("credential-that-must-not-be-logged")
	for _, err := range []error{secret, portalStep("list", secret), portalStep("synow3tool --gen-app", context.DeadlineExceeded)} {
		got := portalFailure(err)
		if strings.Contains(got, secret.Error()) || !strings.Contains(got, "reason=") {
			t.Fatal(got)
		}
	}
	if got := portalFailure(portalStep("list", &dsmError{Code: 105})); got != "step=list reason=dsm-105" {
		t.Fatal(got)
	}
}
