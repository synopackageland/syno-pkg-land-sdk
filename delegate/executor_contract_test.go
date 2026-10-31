package delegate

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// contractServer is the production executor compiled with the contractserver
// test harness (executor/contract_server_test.go): only DSM, peer
// credentials and the bulk writer are substituted.
var contractServer string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "executor-contract-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	contractServer, err = buildContractServer(dir)
	if err != nil {
		os.RemoveAll(dir)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func buildContractServer(dir string) (string, error) {
	source, err := filepath.Abs(filepath.Join("..", "executor"))
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	binary := filepath.Join(dir, "contract-server")
	cmd := exec.CommandContext(ctx, "go", "test", "-race", "-tags", "contractserver", "-c", "-o", binary, ".")
	cmd.Dir = source
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("compile production executor: %v\n%s", err, output)
	}
	return binary, nil
}

func TestRealExecutorAdapterContract(t *testing.T) {
	binary := contractServer
	t.Run("home-owner-commit-and-reusable-grant", func(t *testing.T) {
		f := newContractFixture(t, binary)
		grant := f.grant(t)
		// /homes is not an accepted home-scope File Station vector.
		if _, err := f.s.SelectDestination(context.Background(), destinationCaller(t, "alice"), contractRequest("alice"), SelectDestinationParams{Key: "save", Path: "/homes/alice"}); err == nil {
			t.Fatal("accepted physical homes path")
		}
		in := f.stage(t, grant, "hold-commit")
		out, err := f.s.MoveFromStaging(context.Background(), in)
		if err != nil || out.JobID != in.JobID {
			t.Fatal(out, err)
		}
		f.wait(t, in, "copying")
		ref := OperationRef{grant, in.JobID}
		for _, auth := range []map[string]string{contractAuth("bob"), contractAuth("admin"), {"dsm_cookie": "expired", "dsm_synotoken": "contract-token"}} {
			if _, err = f.s.OperationStatus(context.Background(), auth, ref); err == nil {
				t.Fatal("session fell back to offline status")
			}
			if _, err = f.s.CancelOperation(context.Background(), auth, ref); err == nil {
				t.Fatal("session fell back to offline cancel")
			}
		}
		if err = f.s.verifyOperationOwner(context.Background(), destinationCaller(t, "bob"), contractAuth("bob"), grant); err == nil {
			t.Fatal("other owner admitted")
		}
		if err = f.s.verifyOperationOwner(context.Background(), destinationCaller(t, "alice"), contractAuth("alice"), grant); err != nil {
			t.Fatal(err)
		}
		f.release(t, in.JobID)
		committed := f.wait(t, in, "committed")
		if committed.Receipt == nil || committed.Receipt.Path != "/home/a" || committed.Receipt.Size != 3 {
			t.Fatal(committed)
		}
		if _, err = os.Stat(filepath.Join(f.s.StagingDir, in.Staging)); !os.IsNotExist(err) {
			t.Fatal("committed source not cleaned", err)
		}
		replay, err := f.s.MoveFromStaging(context.Background(), in)
		if err != nil || replay.JobID != in.JobID || replay.State != "committed" {
			t.Fatal(replay, err)
		}
		wire, _ := json.Marshal(replay)
		if strings.Contains(string(wire), "receipt") {
			t.Fatal("admission leaked receipt")
		}
		f.attempts(t, in.JobID, 1)
		// A new operation reuses the permanent grant; Sweep must not revoke it.
		if err = f.s.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		next := f.stage(t, grant, "next-commit")
		if _, err = f.s.MoveFromStaging(context.Background(), next); err != nil {
			t.Fatal(err)
		}
		f.wait(t, next, "committed")
	})
	t.Run("cancel-retains-source", func(t *testing.T) {
		f := newContractFixture(t, binary)
		in := f.stage(t, f.grant(t), "hold-cancel")
		if _, err := f.s.MoveFromStaging(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		f.wait(t, in, "copying")
		if _, err := f.s.CancelOperation(context.Background(), contractAuth("alice"), OperationRef{in.DelegationID, in.JobID}); err != nil {
			t.Fatal(err)
		}
		f.wait(t, in, "canceled")
		if _, err := os.Stat(filepath.Join(f.s.StagingDir, in.Staging)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("queued-restart-uses-original-job", func(t *testing.T) {
		f := newContractFixture(t, binary)
		grant := f.grant(t)
		active := f.stage(t, grant, "hold-active")
		if _, err := f.s.MoveFromStaging(context.Background(), active); err != nil {
			t.Fatal(err)
		}
		f.writing(t, active)
		queued := f.stage(t, grant, "queued-original")
		if _, err := f.s.MoveFromStaging(context.Background(), queued); err != nil {
			t.Fatal(err)
		}
		f.wait(t, queued, "queued")
		f.restart(t)
		f.s.Store = background.NewStore(filepath.Join(filepath.Dir(f.s.StagingDir), "background"))
		f.wait(t, active, "interrupted")
		f.wait(t, queued, "committed")
		if _, err := f.s.MoveFromStaging(context.Background(), queued); err != nil {
			t.Fatal(err)
		}
		f.attempts(t, queued.JobID, 1)
		f.attempts(t, active.JobID, 1)
	})
	t.Run("uncertain-restart-never-replays", func(t *testing.T) {
		f := newContractFixture(t, binary)
		in := f.stage(t, f.grant(t), "uncertain-original")
		if _, err := f.s.MoveFromStaging(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		f.wait(t, in, "uncertain")
		f.restart(t)
		f.s.Store = background.NewStore(filepath.Join(filepath.Dir(f.s.StagingDir), "background"))
		for i := 0; i < 2; i++ {
			out, err := f.s.MoveFromStaging(context.Background(), in)
			if err != nil || out.State != "uncertain" || out.JobID != in.JobID {
				t.Fatal(out, err)
			}
		}
		if err := f.s.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.attempts(t, in.JobID, 1)
		if _, err := os.Stat(filepath.Join(f.s.StagingDir, in.Staging)); err != nil {
			t.Fatal("uncertain source removed", err)
		}
	})
	t.Run("unadmitted-mirror-recovers-once", func(t *testing.T) {
		f := newContractFixture(t, binary)
		grant := f.grant(t)
		unsent := f.stage(t, grant, "unsent-original")
		f.stop()
		_, err := f.s.MoveFromStaging(context.Background(), unsent)
		code(t, err, "EXECUTOR_UNAVAILABLE")
		f.start(t)
		crashed := f.stage(t, grant, "crashed-original")
		reserveWithoutSending(t, f.s, crashed)
		lost := f.stage(t, grant, "lost-original")
		production := f.s.Client
		f.s.Client = fakeExecutor(func(ctx context.Context, op string, auth map[string]string, in, out any) error {
			if err := production.Call(ctx, op, auth, in, out); err != nil || op != "files.moveFromStaging" {
				return err
			}
			return fail("EXECUTOR_PROTOCOL")
		})
		if _, err = f.s.MoveFromStaging(context.Background(), lost); err != nil {
			t.Fatal(err)
		}
		f.s.Client = production
		for _, in := range []MoveParams{unsent, crashed, lost} {
			for i := 0; i < 2; i++ {
				if out, err := f.s.MoveFromStaging(context.Background(), in); err != nil || out.JobID != in.JobID {
					t.Fatal(in.JobID, out, err)
				}
			}
			f.wait(t, in, "committed")
			if out, err := f.s.MoveFromStaging(context.Background(), in); err != nil || out.State != "committed" {
				t.Fatal(in.JobID, out, err)
			}
			f.attempts(t, in.JobID, 1)
		}
	})
	t.Run("copying-cancel-acknowledged-then-canceled", func(t *testing.T) {
		f := newContractFixture(t, binary)
		in := f.stage(t, f.grant(t), "hold-cancel-ack")
		if _, err := f.s.MoveFromStaging(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		f.writing(t, in)
		ref := OperationRef{in.DelegationID, in.JobID}
		out, err := f.s.CancelOperation(context.Background(), nil, ref)
		if err != nil || (out.State != "copying" && out.State != "canceled") || (out.State == "copying" && out.Code != "CANCEL_REQUESTED") {
			t.Fatal("cancel acknowledgement", out, err)
		}
		f.wait(t, in, "canceled")
		f.canceledWithoutPublish(t, in, 1)
	})
	t.Run("cancel-survives-executor-restart", func(t *testing.T) {
		f := newContractFixture(t, binary)
		in := f.stage(t, f.grant(t), "stuck-restart")
		if _, err := f.s.MoveFromStaging(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		f.writing(t, in)
		ref := OperationRef{in.DelegationID, in.JobID}
		for i := 0; i < 2; i++ {
			out, err := f.s.CancelOperation(context.Background(), nil, ref)
			if err != nil || out.State != "copying" || out.Code != "CANCEL_REQUESTED" || out.Reason != "operation-canceled-by-user" {
				t.Fatal("cancel acknowledgement", out, err)
			}
		}
		f.restart(t)
		f.wait(t, in, "canceled")
		f.canceledWithoutPublish(t, in, 1)
	})
	t.Run("cancel-before-admission-is-never-admitted", func(t *testing.T) {
		f := newContractFixture(t, binary)
		in := f.stage(t, f.grant(t), "unsent-canceled")
		reserveWithoutSending(t, f.s, in)
		out, err := f.s.CancelOperation(context.Background(), nil, OperationRef{in.DelegationID, in.JobID})
		if err != nil || out.State != "canceled" || out.Code != "OPERATION_CANCELED" {
			t.Fatal(out, err)
		}
		f.canceledWithoutPublish(t, in, 0)
	})
	t.Run("lost-cancel-reply-retries-same-ref", func(t *testing.T) {
		f := newContractFixture(t, binary)
		in := f.stage(t, f.grant(t), "hold-lost-cancel")
		if _, err := f.s.MoveFromStaging(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		f.writing(t, in)
		ref := OperationRef{in.DelegationID, in.JobID}
		production := f.s.Client
		f.s.Client = fakeExecutor(func(ctx context.Context, op string, auth map[string]string, in, out any) error {
			if err := production.Call(ctx, op, auth, in, out); err != nil || op != "files.cancelOperation" {
				return err
			}
			return fail("EXECUTOR_TIMEOUT")
		})
		_, err := f.s.CancelOperation(context.Background(), nil, ref)
		code(t, err, "EXECUTOR_TIMEOUT")
		f.s.Client = production
		f.wait(t, in, "canceled")
		out, err := f.s.CancelOperation(context.Background(), nil, ref)
		if err != nil || out.State != "canceled" {
			t.Fatal(out, err)
		}
		f.canceledWithoutPublish(t, in, 1)
	})
	t.Run("cancel-after-commit-reports-committed", func(t *testing.T) {
		f := newContractFixture(t, binary)
		in := f.stage(t, f.grant(t), "hold-commit-race")
		if _, err := f.s.MoveFromStaging(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		f.wait(t, in, "copying")
		f.release(t, in.JobID)
		f.wait(t, in, "committed")
		out, err := f.s.CancelOperation(context.Background(), nil, OperationRef{in.DelegationID, in.JobID})
		if err != nil || out.State != "committed" || out.Receipt == nil || out.Receipt.Path != "/home/a" {
			t.Fatal("cancel undid or hid a commit", out, err)
		}
		f.attempts(t, in.JobID, 1)
	})
	t.Run("webapi-call-under-request-deadline", func(t *testing.T) {
		f := newContractFixture(t, binary)
		policy := `{"v":1,"package":"contract","app":"app","operations":["status","delegate.create","delegate.list","delegate.revoke","webapi.call"],"webapi":{"calendar":{"purpose":"Create calendar reminders","apis":{"SYNO.Cal.Event":{"list":[6]}}}}}`
		if err := os.WriteFile(filepath.Join(f.dir, "policy.json"), []byte(policy), 0600); err != nil {
			t.Fatal(err)
		}
		f.restart(t)
		// A CGI request always has a deadline, so the client sends deadline_ms.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		grant, err := CreateWebAPIGrant(ctx, f.s.Client, contractAuth("alice"), "calendar", WebAPIGrantOptions{UntilRevoked: true})
		if err != nil {
			t.Fatal(err)
		}
		remaining := func(ctx context.Context) int64 {
			t.Helper()
			out, err := JobContext{Client: f.s.Client, DelegationID: grant.DelegationID}.WebAPI().Call(ctx, "SYNO.Cal.Event", "list", 6, nil)
			var envelope struct {
				Data struct {
					RemainingMS int64 `json:"remaining_ms"`
				} `json:"data"`
			}
			if err != nil || json.Unmarshal(out, &envelope) != nil {
				t.Fatalf("webapi.call: %s %v", out, err)
			}
			return envelope.Data.RemainingMS
		}
		// The executor bounds the call by the request's deadline, and keeps
		// its own 10-second WebAPI bound without one.
		if got := remaining(ctx); got <= 0 || got > 2000 {
			t.Fatalf("deadline_ms not applied: %d ms left", got)
		}
		if got := remaining(context.Background()); got <= 2000 {
			t.Fatalf("call without deadline_ms bounded to %d ms", got)
		}
	})
	t.Run("http-owner-and-admin-management", func(t *testing.T) {
		f := newContractFixture(t, binary)
		grant := f.grant(t)
		in := f.stage(t, grant, "hold-http")
		// A different owner cannot reserve the package-side mirror, even with
		// a valid DSM session. The owner can use the same untouched job ID.
		f.rpc(t, "bob", "delegate.move_from_staging", in, false)
		if _, err := f.s.Store.MoveBinding(context.Background(), in.JobID); !os.IsNotExist(err) {
			t.Fatal("other owner reserved mirror", err)
		}
		f.rpc(t, "alice", "delegate.move_from_staging", in, true)
		f.wait(t, in, "copying")
		ref := OperationRef{grant, in.JobID}
		f.rpc(t, "bob", "delegate.operations.get", ref, false)
		f.rpc(t, "admin", "delegate.operations.cancel", ref, false)
		f.rpc(t, "alice", "delegate.operations.get", ref, true)
		f.rpc(t, "alice", "delegate.disable", struct{}{}, false)
		f.rpc(t, "admin", "delegate.disable", struct{}{}, true)
		f.wait(t, in, "canceled")
		f.restart(t)
		if _, err := f.s.SelectDestination(context.Background(), destinationCaller(t, "alice"), contractRequest("alice"), SelectDestinationParams{Key: "save", Path: "/home"}); err == nil {
			t.Fatal("restart cleared disabled flag")
		}
		f.rpc(t, "admin", "delegate.audit", struct{}{}, true)
		f.rpc(t, "admin", "delegate.enable", struct{}{}, true)
		stale := f.stage(t, grant, "revoked-original")
		if _, err := f.s.MoveFromStaging(context.Background(), stale); err == nil {
			t.Fatal("enable revived old grant")
		}
		fresh := f.stage(t, f.grant(t), "after-enable")
		if _, err := f.s.MoveFromStaging(context.Background(), fresh); err != nil {
			t.Fatal(err)
		}
		f.wait(t, fresh, "committed")
	})
}

type contractIdentity string

func (i contractIdentity) Authenticate(*http.Request) (api.Identity, error) {
	return api.Identity{User: string(i), Admin: i == "admin"}, nil
}

func (f *contractFixture) rpc(t *testing.T, user, method string, params any, success bool) {
	t.Helper()
	methods := map[string]string{}
	// Exercise the shipped SDK roles as well as session/owner/admin checks.
	policy, _ := json.Marshal(map[string]any{"schema": 1, "default": "administrators", "methods": methods})
	server, err := api.NewServerWithPermissions(contractIdentity(user), policy, f.s.Routes()...)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"method": method, "params": params})
	req := httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
	req.Header = contractRequest(user).Header.Clone()
	req.Header.Set("Origin", "http://"+req.Host)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	var out struct {
		Success bool `json:"success"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &out) != nil || out.Success != success {
		t.Fatalf("%s %s: %s", user, method, recorder.Body.String())
	}
}

type contractFixture struct {
	s           *Service
	dir, binary string
	process     *exec.Cmd
}

func newContractFixture(t *testing.T, binary string) *contractFixture {
	t.Helper()
	// Keep the Unix socket below Darwin's path length limit.
	dir, err := os.MkdirTemp("/tmp", "w2-contract-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "home"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "protected"), 0700); err != nil {
		t.Fatal(err)
	}
	f := &contractFixture{s: service(t, nil), dir: dir, binary: binary}
	f.s.Client = &Client{Socket: filepath.Join(dir, "executor.sock"), Timeout: 3 * time.Second}
	f.s.SessionFiles = &fakeSession{}
	f.s.Destinations = []string{"save"}
	f.s.Operations = []string{"files.moveFromStaging"}
	f.s.Lifetime = "until-revoked"
	t.Cleanup(func() {
		f.stop()
		log, err := os.ReadFile(filepath.Join(f.dir, "server.log"))
		if err != nil || strings.Contains(string(log), "DATA RACE") || strings.Contains(string(log), "FAIL") {
			t.Fatalf("executor subprocess failed: %v\n%s", err, log)
		}
	})
	f.start(t)
	return f
}
func (f *contractFixture) start(t *testing.T) {
	t.Helper()
	os.Remove(filepath.Join(f.dir, "executor.sock"))
	log, err := os.OpenFile(filepath.Join(f.dir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	f.process = exec.Command(f.binary, "-test.run=^TestContractServer$", "-test.timeout=2m")
	f.process.Env = append(os.Environ(), "CONTRACT_DIR="+f.dir)
	f.process.Stdout = log
	f.process.Stderr = log
	if err = f.process.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, e := net.DialTimeout("unix", filepath.Join(f.dir, "executor.sock"), 50*time.Millisecond)
		if e == nil {
			c.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	output, _ := os.ReadFile(filepath.Join(f.dir, "server.log"))
	t.Fatalf("executor did not start: %s", output)
}
func (f *contractFixture) stop() {
	if f.process != nil {
		f.process.Process.Kill()
		f.process.Wait()
		f.process = nil
	}
}
func (f *contractFixture) restart(t *testing.T) { t.Helper(); f.stop(); f.start(t) }
func contractAuth(user string) map[string]string {
	return map[string]string{"dsm_cookie": user, "dsm_synotoken": "contract-token"}
}
func contractRequest(user string) *http.Request {
	r := destinationRequest()
	r.Header.Set("Cookie", "id="+user)
	r.Header.Set("X-SYNO-TOKEN", "contract-token")
	return r
}
func (f *contractFixture) grant(t *testing.T) string {
	t.Helper()
	out, err := f.s.SelectDestination(context.Background(), destinationCaller(t, "alice"), contractRequest("alice"), SelectDestinationParams{Key: "save", Path: "/home"})
	if err != nil || out.ID == "" || out.Path != "/home" {
		t.Fatal(out, err)
	}
	return out.ID
}
func (f *contractFixture) stage(t *testing.T, grant, job string) MoveParams {
	t.Helper()
	source := OwnerStagingPrefix("alice", grant) + job
	if err := os.MkdirAll(filepath.Dir(filepath.Join(f.s.StagingDir, source)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.s.StagingDir, source), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	return MoveParams{DelegationID: grant, JobID: job + OwnerJobSuffix("alice", grant), Staging: source, Name: "a", Size: 3}
}
func (f *contractFixture) wait(t *testing.T, in MoveParams, state string) OperationResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var out OperationResult
	var err error
	for time.Now().Before(deadline) {
		out, err = f.s.OperationStatus(context.Background(), nil, OperationRef{in.DelegationID, in.JobID})
		if err != nil {
			t.Fatal(err)
		}
		if out.State != "committed" && out.Receipt != nil {
			t.Fatal("noncommitted receipt", out)
		}
		if out.State == state {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("wanted %s, got %+v (%v)", state, out, err)
	return out
}

// writing waits until the operation is copying and the bulk writer has
// recorded its attempt. The executor journals copying before it calls the
// writer, so copying alone does not mean a cancel or restart lands in the write.
func (f *contractFixture) writing(t *testing.T, in MoveParams) {
	t.Helper()
	f.wait(t, in, "copying")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(f.dir, "attempt-"+in.JobID)); err == nil && strings.Count(string(b), "attempt\n") == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("writer did not start", in.JobID)
}
func (f *contractFixture) release(t *testing.T, job string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "release-"+job), nil, 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *contractFixture) attempts(t *testing.T, job string, want int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "attempt-"+job))
	if want == 0 && os.IsNotExist(err) {
		return
	}
	if err != nil || strings.Count(string(b), "attempt\n") != want {
		t.Fatal("writer replay", job, string(b), err)
	}
}

// canceledWithoutPublish asserts the terminal cancel contract: the same ref
// stays canceled, recovery never admits it again, nothing was published and
// the package staging source is kept.
func (f *contractFixture) canceledWithoutPublish(t *testing.T, in MoveParams, attempts int) {
	t.Helper()
	ref := OperationRef{in.DelegationID, in.JobID}
	for i := 0; i < 2; i++ {
		if out, err := f.s.CancelOperation(context.Background(), nil, ref); err != nil || out.State != "canceled" || out.Code != "OPERATION_CANCELED" {
			t.Fatal("repeated cancel", out, err)
		}
		if out, err := f.s.MoveFromStaging(context.Background(), in); err != nil || out.State != "canceled" {
			t.Fatal("same-ref move after cancel", out, err)
		}
		if err := f.s.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	f.attempts(t, in.JobID, attempts)
	if _, err := os.Stat(filepath.Join(f.dir, "home", in.Name)); !os.IsNotExist(err) {
		t.Fatal("canceled operation published", err)
	}
	if _, err := os.Stat(filepath.Join(f.s.StagingDir, in.Staging)); err != nil {
		t.Fatal("canceled source removed", err)
	}
}
