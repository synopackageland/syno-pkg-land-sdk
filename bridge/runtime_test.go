//go:build unix

package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/delegate"
	"github.com/synopackageland/syno-pkg-land-sdk/delegate/delegatetest"
)

// appDir writes an App directory: bridge.json from manifest, permissions.json
// from policy, and links (name -> target) for the App's files.
func appDir(t *testing.T, manifest, policy string, links map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ManifestFile), []byte(manifest), 0o600)
	os.WriteFile(filepath.Join(dir, PermissionsFile), []byte(policy), 0o600)
	for name, target := range links {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700)
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runtimeFor(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	if cfg.PackageID == "" {
		cfg.PackageID = "hello"
	}
	if cfg.Auth == nil {
		cfg.Auth = userAuth{api.Identity{User: "alice"}}
	}
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	h, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

const helloPolicy = `{"schema":1,"default":"administrators","methods":{"hello.greet":"authenticated","hello.save":"authenticated"}}`

// The hello examples, end to end through the runtime in exec mode, as the
// package's CGI would serve them.
func TestHelloExamples(t *testing.T) {
	for _, ex := range []struct {
		name   string
		config func(t *testing.T) (manifest string, links map[string]string, cfg Config)
	}{
		{"python", func(t *testing.T) (string, map[string]string, Config) {
			return `{"schema":1,"runtime":"python","entry":"app.py","mode":"exec","methods":["hello.greet","hello.save"],"env":{"PYTHONPATH":"` + filepath.Join(root(t), "python") + `"}}`,
				map[string]string{"app.py": filepath.Join(root(t), "python/examples/hello/app.py")},
				Config{Python: interpreter(t, "python3")}
		}},
		{"node", func(t *testing.T) (string, map[string]string, Config) {
			packages := t.TempDir()
			bin := filepath.Join(packages, "Node.js_v20/target/usr/local/bin")
			os.MkdirAll(bin, 0o700)
			os.Symlink(interpreter(t, "node"), filepath.Join(bin, "node"))
			return `{"schema":1,"runtime":"node","entry":"app.mjs","mode":"exec","methods":["hello.greet","hello.save"],"node":{"package":"Node.js_v20"}}`,
				map[string]string{"app.mjs": filepath.Join(root(t), "node/examples/hello/app.mjs")},
				Config{PackagesDir: packages}
		}},
		{"rust", func(t *testing.T) (string, map[string]string, Config) {
			return `{"schema":1,"runtime":"rust","entry":"bin/hello","mode":"exec","methods":["hello.greet","hello.save"]}`,
				map[string]string{"bin/hello": rustExample(t, "hello")}, Config{}
		}},
	} {
		t.Run(ex.name, func(t *testing.T) {
			manifest, links, cfg := ex.config(t)
			cfg.AppDir, cfg.DataDir = appDir(t, manifest, helloPolicy, links), t.TempDir()
			srv := runtimeFor(t, cfg)
			if got := serve(t, srv, `{"method":"hello.greet"}`, nil); got != `{"data":{"message":"Hello, alice"},"success":true}`+"\n" {
				t.Fatalf("greet %s", got)
			}
			if got := serve(t, srv, `{"method":"hello.save","params":{"note":"hi"}}`, nil); got != `{"data":{"bytes":2},"success":true}`+"\n" {
				t.Fatalf("save %s", got)
			}
			if got := serve(t, srv, `{"method":"hello.save","params":{}}`, nil); got != `{"error":{"code":"bad_request","message":"note must be a string"},"success":false}`+"\n" {
				t.Fatalf("save %s", got)
			}
			notes, _ := os.ReadDir(filepath.Join(cfg.DataDir, "notes"))
			if len(notes) != 1 {
				t.Fatalf("notes %v", notes)
			}
		})
	}
}

// helperApp writes an App directory whose entry is the test binary, run as a
// rust App: BRIDGE_TEST_APP in its environment makes it helperApp ("env",
// method env.dump) or stallApp ("stall", method a.b); see TestMain.
func helperAppDir(t *testing.T, mode, extra, policy string) string {
	method, env := "env.dump", `"BRIDGE_TEST_APP":"env"`
	if mode == "stall" {
		method, env = "a.b", `"BRIDGE_TEST_APP":"stall","STALL_PID_FILE":"`+filepath.Join(t.TempDir(), "pid")+`"`
	}
	manifest := `{"schema":1,"runtime":"rust","entry":"app","mode":"exec","methods":["` + method + `"],"env":{` + env + `}` + extra + `}`
	return appDir(t, manifest, policy, map[string]string{"app": os.Args[0]})
}

func TestTheAppGetsOnlyTheRuntimeEnvironment(t *testing.T) {
	t.Setenv("HTTP_COOKIE", "id=secret")
	dir := helperAppDir(t, "env", "", `{"schema":1,"default":"administrators","methods":{"env.dump":"authenticated"}}`)
	data := t.TempDir()
	body := serve(t, runtimeFor(t, Config{AppDir: dir, DataDir: data}), `{"method":"env.dump"}`, nil)
	var reply struct{ Data []string }
	json.Unmarshal([]byte(body), &reply)
	slices.Sort(reply.Data)
	want := []string{"BRIDGE_TEST_APP=env", "PATH=/usr/local/bin:/usr/bin:/bin", "SYNOPKGLAND_BRIDGE=stdio", "SYNOPKGLAND_DATA_DIR=" + data, "SYNOPKGLAND_PACKAGE=hello"}
	if !slices.Equal(reply.Data, want) {
		t.Fatalf("env %v (%s)", reply.Data, body)
	}
}

func TestRuntimeRefusesFilesTheBuildDidNotValidate(t *testing.T) {
	policy := `{"schema":1,"default":"administrators","methods":{}}`
	dir := helperAppDir(t, "env", "", policy)
	manifest, _ := os.ReadFile(filepath.Join(dir, ManifestFile))
	digest := func(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
	for name, cfg := range map[string]Config{
		"manifest":    {PackageID: "hello", ManifestDigest: digest([]byte("other"))},
		"permissions": {PackageID: "hello", PermissionsDigest: digest([]byte("other"))},
		"package":     {PackageID: "../x"},
	} {
		cfg.AppDir, cfg.Auth = dir, userAuth{}
		if _, err := NewRuntime(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewRuntime(Config{PackageID: "hello", AppDir: dir, DataDir: t.TempDir(), Auth: userAuth{}, ManifestDigest: digest(manifest), PermissionsDigest: digest([]byte(policy))}); err != nil {
		t.Fatal(err)
	}
}

func TestAdminOnlyAccessKeepsOthersFromTheApp(t *testing.T) {
	dir := helperAppDir(t, "env", `,"access":"admin-only"`, `{"schema":1,"default":"administrators","methods":{"env.dump":"authenticated"}}`)
	if got := serve(t, runtimeFor(t, Config{AppDir: dir}), `{"method":"env.dump"}`, nil); !strings.Contains(got, `"code":"admin_required"`) {
		t.Fatalf("user: %s", got)
	}
	admin := runtimeFor(t, Config{AppDir: dir, Auth: userAuth{api.Identity{User: "root", Admin: true}}})
	if got := serve(t, admin, `{"method":"env.dump"}`, nil); !strings.Contains(got, `"success":true`) {
		t.Fatalf("admin: %s", got)
	}
}

func TestManifestTimeoutEndsTheCall(t *testing.T) {
	dir := helperAppDir(t, "stall", `,"timeout_seconds":1`, `{"schema":1,"default":"administrators","methods":{"a.b":"authenticated"}}`)
	start := time.Now()
	got := serve(t, runtimeFor(t, Config{AppDir: dir}), `{"method":"a.b"}`, nil)
	if !strings.Contains(got, `"code":"request_timeout"`) || time.Since(start) > 5*time.Second {
		t.Fatalf("%v: %s", time.Since(start), got)
	}
}

func TestCapabilitiesAddSDKRoutes(t *testing.T) {
	policy := `{"schema":1,"default":"administrators","methods":{}}`
	off := runtimeFor(t, Config{AppDir: helperAppDir(t, "env", "", policy)})
	on := runtimeFor(t, Config{AppDir: helperAppDir(t, "env", `,"capabilities":{"files":"session","portal":true}`, policy)})
	for _, method := range []string{"portal.status", "nas.session.status"} {
		if got := serve(t, off, `{"method":"`+method+`"}`, nil); !strings.Contains(got, `"code":"unknown_method"`) {
			t.Errorf("off %s: %s", method, got)
		}
		if got := serve(t, on, `{"method":"`+method+`"}`, nil); strings.Contains(got, "unknown_method") {
			t.Errorf("on %s: %s", method, got)
		}
	}
}

func TestIdentityNeedsTheCGIPath(t *testing.T) {
	dir := helperAppDir(t, "env", `,"capabilities":{"identity":"standalone"}`, `{"schema":1,"default":"administrators","methods":{}}`)
	if _, err := NewRuntime(Config{PackageID: "hello", AppDir: dir, DataDir: t.TempDir(), Auth: userAuth{}, ScriptName: "api.cgi"}); err == nil {
		t.Fatal("accepted a relative SCRIPT_NAME")
	}
	h := runtimeFor(t, Config{AppDir: dir, ScriptName: "/webman/3rdparty/hello/cgi/api.cgi"})
	if got := serve(t, h, `{"method":"identity.status"}`, nil); strings.Contains(got, "unknown_method") {
		t.Fatalf("identity.status %s", got)
	}
}

func TestTransfersAreHTTPTransferRoutesSpooledInTheDataDirectory(t *testing.T) {
	dir := helperAppDir(t, "env", `,"transfers":{"env.dump":"download"}`, `{"schema":1,"default":"administrators","methods":{"env.dump":"authenticated"}}`)
	data := t.TempDir()
	srv := runtimeFor(t, Config{AppDir: dir, DataDir: data})
	if got := serve(t, srv, `{"method":"env.dump"}`, nil); !strings.Contains(got, "use HTTP transfer endpoint") {
		t.Fatalf("JSON call: %s", got)
	}
	r := httptest.NewRequest("GET", "https://nas.local:5001/api.cgi?method=env.dump", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	// The helper answers a list, not a file: the runtime refuses it.
	if !strings.Contains(w.Body.String(), `"code":"internal"`) {
		t.Fatalf("download: %s", w.Body)
	}
	if info, err := os.Stat(filepath.Join(data, SpoolDir)); err != nil || !info.IsDir() {
		t.Fatalf("spool: %v", err)
	}
}

func TestCapabilitiesOfferServices(t *testing.T) {
	m, err := ParseManifest([]byte(strings.Replace(validManifest, `"identity": "standalone", `, ``, 1)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{PackageID: "hello", DataDir: t.TempDir()}.withDefaults()
	var got []string
	for op := range cfg.services(m).table() {
		got = append(got, op)
	}
	slices.Sort(got)
	want := []string{"bg.cancel", "bg.get", "bg.submit", "delegate.discard_staging", "delegate.move_from_staging", "delegate.operation", "delegate.status",
		"files.info", "files.list", "files.read", "portal.status", "root.jobs.get", "root.jobs.list", "root.jobs.stop", "root.run", "root.start"}
	if !slices.Equal(got, want) {
		t.Fatalf("services %v", got)
	}
	if got := cfg.services(Manifest{}).table(); len(got) != 0 {
		t.Fatalf("no capabilities: %v", got)
	}
}

func TestDelegateCapabilityServesTheBrowserRoutes(t *testing.T) {
	policy := `{"schema":1,"default":"administrators","methods":{}}`
	digest := strings.Repeat("a", 64)
	h := runtimeFor(t, Config{AppDir: helperAppDir(t, "env", `,"capabilities":{"delegate":{"scope":"home","destinations":["save"],"executor_digest":"`+digest+`","policy_digest":"`+digest+`"}}`, policy)})
	if got := serve(t, h, `{"method":"delegate.status"}`, nil); strings.Contains(got, "unknown_method") {
		t.Fatalf("delegate.status %s", got)
	}
	off := runtimeFor(t, Config{AppDir: helperAppDir(t, "env", "", policy)})
	if got := serve(t, off, `{"method":"delegate.status"}`, nil); !strings.Contains(got, "unknown_method") {
		t.Fatalf("without the capability: %s", got)
	}
}

// An older Python than the SDK supports leaves the App unavailable; why is
// in the log only.
func TestPythonOlderThan38IsUnavailable(t *testing.T) {
	bin := t.TempDir()
	probed := filepath.Join(bin, "python")
	os.WriteFile(probed, []byte("#!/bin/sh\necho 3 7\n"), 0o700)
	named := filepath.Join(bin, "python3.6")
	os.WriteFile(named, []byte("#!/bin/sh\necho 3 12\n"), 0o700)
	os.Symlink(named, filepath.Join(bin, "python3"))
	for python, version := range map[string]string{probed: "3.7", filepath.Join(bin, "python3"): "3.6"} {
		var logged bytes.Buffer
		log.SetOutput(&logged)
		dir := appDir(t, `{"schema":1,"runtime":"python","entry":"app.py","mode":"exec","methods":["hello.greet","hello.save"]}`, helloPolicy, nil)
		got := serve(t, runtimeFor(t, Config{AppDir: dir, Python: python}), `{"method":"hello.greet"}`, nil)
		log.SetOutput(os.Stderr)
		if !strings.Contains(got, `"code":"app_unavailable"`) || strings.Contains(got, version) || !strings.Contains(logged.String(), "Python "+version) {
			t.Errorf("%s: %s (log %q)", python, got, logged.String())
		}
	}
}

// An App's e2e test serves the executor with delegatetest, without root.
func TestATestServesTheExecutorOnItsOwnSocket(t *testing.T) {
	e := delegatetest.NewExecutor(t, nil)
	policy := `{"schema":1,"default":"administrators","methods":{}}`
	digest := strings.Repeat("a", 64)
	h := runtimeFor(t, Config{ExecutorSocket: e.Socket, AppDir: helperAppDir(t, "env", `,"capabilities":{"delegate":{"scope":"home","destinations":["save"],"executor_digest":"`+digest+`","policy_digest":"`+digest+`"}}`, policy)})
	serve(t, h, `{"method":"delegate.status","params":{}}`, dsmHeaders)
	if seen := e.Requests(); len(seen) == 0 || seen[0].Auth["dsm_cookie"] != "sid1" {
		t.Fatalf("executor requests %+v", seen)
	}
}

// capabilities.root serves the routes the CLI's root window calls after
// approval: the App has no way to know the executor's pins.
func TestRootCapabilityServesExecutorStatusAndControl(t *testing.T) {
	e := delegatetest.NewExecutor(t, func(q delegate.Request) string {
		if q.Op != "status" {
			return delegatetest.Refused(delegate.CodeUnknownOp)
		}
		return delegatetest.OK(`{"ready":true,"state":"ready","executor_digest":"e1","policy_digest":"p1"}`)
	})
	policy := `{"schema":1,"default":"administrators","methods":{}}`
	dir := helperAppDir(t, "env", `,"capabilities":{"root":true}`, policy)
	data := t.TempDir()
	user := runtimeFor(t, Config{ExecutorSocket: e.Socket, AppDir: dir, DataDir: data})
	if got := serve(t, user, `{"method":"executor.status"}`, nil); !strings.Contains(got, `"executor_digest":"e1"`) || !strings.Contains(got, `"policy_digest":"p1"`) {
		t.Fatalf("status: %s", got)
	}
	if got := serve(t, user, `{"method":"executor.control","params":{"action":"start"}}`, nil); !strings.Contains(got, `"code":"admin_required"`) {
		t.Fatalf("user control: %s", got)
	}
	if _, err := os.Stat(filepath.Join(data, "executor")); !os.IsNotExist(err) {
		t.Fatal("a refused control wrote the mailbox")
	}
	admin := runtimeFor(t, Config{ExecutorSocket: e.Socket, AppDir: dir, DataDir: data, Auth: userAuth{api.Identity{User: "root", Admin: true}}})
	if got := serve(t, admin, `{"method":"executor.control","params":{"action":"start"}}`, nil); !strings.Contains(got, `"written":true`) {
		t.Fatalf("admin control: %s", got)
	}
	if b, err := os.ReadFile(filepath.Join(data, "executor", "control")); err != nil || string(b) != "start" {
		t.Fatalf("mailbox %q %v", b, err)
	}
	// Approval's start does not reach the executor: the package lifecycle reads the mailbox.
	for _, q := range e.Requests() {
		if q.Op != "status" {
			t.Fatalf("executor op %s", q.Op)
		}
	}
}

func TestExecutorStatusIsPausedWhileTheExecutorDoesNotListen(t *testing.T) {
	policy := `{"schema":1,"default":"administrators","methods":{}}`
	h := runtimeFor(t, Config{ExecutorSocket: filepath.Join(t.TempDir(), "absent.sock"), AppDir: helperAppDir(t, "env", `,"capabilities":{"root":true}`, policy)})
	if got := serve(t, h, `{"method":"executor.status"}`, nil); !strings.Contains(got, `"state":"paused"`) || !strings.Contains(got, `"reason":"executor_not_listening"`) {
		t.Fatalf("status: %s", got)
	}
}

// A stopped package's executor.status refuses APP_STOPPED, as its root
// services do, so the window does not ask for approval.
func TestExecutorStatusOfAStoppedPackageIsAppStopped(t *testing.T) {
	policy := `{"schema":1,"default":"administrators","methods":{}}`
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "executor"), 0o700)
	os.WriteFile(filepath.Join(data, "executor", "control"), []byte("stop"), 0o600)
	h := runtimeFor(t, Config{ExecutorSocket: filepath.Join(t.TempDir(), "absent.sock"), DataDir: data, AppDir: helperAppDir(t, "env", `,"capabilities":{"root":true}`, policy)})
	if got := serve(t, h, `{"method":"executor.status"}`, nil); !strings.Contains(got, `"code":"APP_STOPPED"`) {
		t.Fatalf("status: %s", got)
	}
}

func TestExecutorRoutesNeedTheRootCapability(t *testing.T) {
	policy := `{"schema":1,"default":"administrators","methods":{}}`
	h := runtimeFor(t, Config{AppDir: helperAppDir(t, "env", "", policy), Auth: userAuth{api.Identity{User: "root", Admin: true}}})
	for _, method := range []string{"executor.status", "executor.control"} {
		if got := serve(t, h, `{"method":"`+method+`"}`, nil); !strings.Contains(got, `"code":"unknown_method"`) {
			t.Errorf("%s: %s", method, got)
		}
	}
}

// Root services read the package's lifecycle mailbox, so a stopped package's
// App gets APP_STOPPED instead of EXECUTOR_UNAVAILABLE; executor.status
// reads the mailbox itself.
func TestRootServicesKnowThePackageLifecycle(t *testing.T) {
	data := t.TempDir()
	sv := Config{PackageID: "MyApp", DataDir: data}.services(Manifest{Capabilities: Capabilities{Root: true}})
	c, ok := sv.executor.(*delegate.Client)
	if !ok || c.Mailbox != filepath.Join(data, "executor") {
		t.Fatalf("root client %#v", sv.executor)
	}
}

// the delegate service's and executor routes' client read no
// mailbox, so a stopped package's delegate.status was EXECUTOR_UNAVAILABLE.
// Every executor client of the runtime reads the package's lifecycle mailbox.
func TestEveryExecutorClientKnowsThePackageLifecycle(t *testing.T) {
	data := t.TempDir()
	if got := (Config{PackageID: "MyApp", DataDir: data, ExecutorSocket: "/s"}).executor(); got.Mailbox != filepath.Join(data, "executor") || got.Socket != "/s" {
		t.Fatalf("client %#v", got)
	}
}
