package delegate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type executorUser struct{ api.Identity }

func (u executorUser) Authenticate(*http.Request) (api.Identity, error) { return u.Identity, nil }

// executorCall serves ExecutorRoutes as a package CGI does, with the SDK's
// own permissions, and answers the response envelope.
func executorCall(t *testing.T, c ExecutorClient, dataDir string, who api.Identity, method, params string) map[string]any {
	t.Helper()
	srv, err := api.NewServerWithPermissions(executorUser{who}, []byte(`{"schema":1,"default":"administrators","methods":{}}`), ExecutorRoutes(c, dataDir)...)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"method":"`+method+`","params":`+params+`}`))
	r.Header.Set("Origin", "http://"+r.Host)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %s", method, w.Body)
	}
	return out
}

func envelopeCode(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

var (
	executorAdmin = api.Identity{User: "root", Admin: true}
	executorAlice = api.Identity{User: "alice"}
)

func TestExecutorStatusAnswersTheExecutorsOwnPins(t *testing.T) {
	var ops []string
	c := fakeExecutor(func(_ context.Context, op string, auth map[string]string, _, out any) error {
		ops = append(ops, op)
		return json.Unmarshal([]byte(`{"ready":true,"state":"ready","executor_digest":"e1","policy_digest":"p1"}`), out)
	})
	out := executorCall(t, c, t.TempDir(), executorAlice, "executor.status", `{}`)
	data, _ := out["data"].(map[string]any)
	if out["success"] != true || data["executor_digest"] != "e1" || data["policy_digest"] != "p1" || data["state"] != "ready" {
		t.Fatalf("status %v", out)
	}
	if len(ops) != 1 || ops[0] != "status" {
		t.Fatalf("executor ops %v", ops)
	}
}

// The executor closes its socket while paused: an upgrade changed the pins and
// awaits approval, or a start found no executor. A start that ran longer ago
// than StartGrace tells it from a package that never asked to start.
func TestExecutorStatusIsPausedWhileTheExecutorDoesNotListen(t *testing.T) {
	data := t.TempDir()
	c := &Client{Socket: filepath.Join(data, "absent.sock")}
	next := func() string {
		out := executorCall(t, c, data, executorAlice, "executor.status", `{}`)
		got, _ := out["data"].(map[string]any)
		if out["success"] != true || got["ready"] != false || got["state"] != "paused" || got["reason"] != "executor_not_listening" {
			t.Fatalf("status %v", out)
		}
		s, _ := got["next"].(string)
		return s
	}
	if !strings.HasPrefix(next(), "Start the package.") {
		t.Fatal("no request:", next())
	}
	os.MkdirAll(filepath.Join(data, "executor"), 0o700)
	control := filepath.Join(data, "executor", "control")
	for _, request := range []string{"start\n", "start"} {
		os.WriteFile(control, []byte(request), 0o600)
		past := time.Now().Add(-StartGrace - time.Second)
		os.Chtimes(control, past, past)
		if got := next(); strings.HasPrefix(got, "Start the package") || got == "" {
			t.Fatalf("%q: %s", request, got)
		}
	}
}

// A stopped package, and one that asked to start less than StartGrace ago,
// refuse as delegate.status does (APP_STOPPED, APP_STARTING): the CLI's root
// window and the SDK's panels then say so and check again, instead of asking
// for approval.
func TestExecutorStatusRefusesWhileThePackageIsStoppedOrStarting(t *testing.T) {
	data := t.TempDir()
	c := &Client{Socket: filepath.Join(data, "absent.sock")}
	os.MkdirAll(filepath.Join(data, "executor"), 0o700)
	for request, code := range map[string]string{"stop": CodeAppStopped, "stop\n": CodeAppStopped, "start": CodeAppStarting} {
		os.WriteFile(filepath.Join(data, "executor", "control"), []byte(request), 0o600)
		if out := executorCall(t, c, data, executorAlice, "executor.status", `{}`); envelopeCode(out) != code {
			t.Fatalf("%q: %v", request, out)
		}
	}
}

func TestExecutorStatusPassesOtherRefusalsOn(t *testing.T) {
	c := fakeExecutor(func(context.Context, string, map[string]string, any, any) error { return fail(CodeExecutorTimeout) })
	if out := executorCall(t, c, t.TempDir(), executorAlice, "executor.status", `{}`); envelopeCode(out) != CodeExecutorTimeout {
		t.Fatalf("status %v", out)
	}
}

func TestExecutorControlWritesOnlyFixedActionsToTheLifecycleMailbox(t *testing.T) {
	data := t.TempDir()
	for _, action := range []string{"start", "stop", "purge"} {
		out := executorCall(t, nil, data, executorAdmin, "executor.control", `{"action":"`+action+`"}`)
		if out["success"] != true {
			t.Fatalf("%s: %v", action, out)
		}
		if b, err := os.ReadFile(filepath.Join(data, "executor", "control")); err != nil || string(b) != action {
			t.Fatalf("%s: mailbox %q %v", action, b, err)
		}
	}
	for _, params := range []string{`{"action":"start;id"}`, `{"action":"start\nstop"}`, `{"action":""}`, `{"action":"stop","path":"/etc/passwd"}`, `{}`} {
		if out := executorCall(t, nil, data, executorAdmin, "executor.control", params); envelopeCode(out) != "bad_request" {
			t.Fatalf("accepted %s: %v", params, out)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(data, "executor", "control")); string(b) != "purge" {
		t.Fatalf("a refused action changed the mailbox: %q", b)
	}
}

func TestExecutorControlIsForAdministratorsOnly(t *testing.T) {
	data := t.TempDir()
	if out := executorCall(t, nil, data, executorAlice, "executor.control", `{"action":"stop"}`); envelopeCode(out) != "admin_required" {
		t.Fatalf("user: %v", out)
	}
	if _, err := os.Stat(filepath.Join(data, "executor")); !os.IsNotExist(err) {
		t.Fatal("a refused caller created the mailbox")
	}
}

func TestExecutorControlRefusesALinkedMailbox(t *testing.T) {
	data, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(data, "executor")); err != nil {
		t.Fatal(err)
	}
	if out := executorCall(t, nil, data, executorAdmin, "executor.control", `{"action":"stop"}`); out["success"] == true {
		t.Fatalf("accepted a linked mailbox: %v", out)
	}
	if _, err := os.Stat(filepath.Join(outside, "control")); !os.IsNotExist(err) {
		t.Fatal("wrote outside the data directory")
	}
}
