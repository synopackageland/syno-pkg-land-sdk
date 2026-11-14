package selfrestart

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type approver struct {
	calls  atomic.Int32
	digest string
	refuse bool
}

func (a *approver) Consume(_ context.Context, c api.Caller, ref, digest, handle string) error {
	if !c.Admin() || !reference.MatchString(ref) || digest == "" || handle != "protected-one-use" || a.refuse {
		return ErrRefused
	}
	if a.digest != "" && a.digest != digest {
		return ErrRefused
	}
	a.calls.Add(1)
	return nil
}

func fixture(t *testing.T) (*Service, *approver) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	b := []byte(`{"schema":1,"package":"Example","version":"1.0.0-0001","enabled":true}`)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	approval := &approver{}
	s, err := New(Config{PolicyPath: path, PolicySHA256: hex.EncodeToString(h[:]), StateDir: filepath.Join(dir, "state"), Approval: approval, ActiveWork: func(context.Context) ([]Work, error) { return []Work{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return s, approval
}

type auth struct {
	user  string
	admin bool
}

func (a auth) Authenticate(*http.Request) (api.Identity, error) {
	return api.Identity{User: a.user, Admin: a.admin}, nil
}

func caller(t *testing.T, user string, admin bool) api.Caller {
	t.Helper()
	var out api.Caller
	s, err := api.NewServerWithPermissions(auth{user, admin}, []byte(`{"schema":1,"default":"administrators","methods":{"test.capture":"authenticated"}}`), api.Handle("test.capture", func(_ context.Context, c api.Caller, _ json.RawMessage) (any, error) { out = c; return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://example.test/cgi", bytes.NewBufferString(`{"method":"test.capture","params":{}}`))
	r.Header.Set("Origin", "http://example.test")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if !out.Authenticated() {
		t.Fatalf("capture: %s", w.Body.String())
	}
	return out
}

func TestConfirmSpendsSingleDurablePermitAcrossInstances(t *testing.T) {
	s, a := fixture(t)
	c := caller(t, "operator", true)
	ctx := context.Background()
	draft, err := s.Draft(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	var dispatched atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Independent Service instances, as with separate CGI processes.
			fresh, err := New(s.config)
			if err != nil {
				t.Error(err)
				return
			}
			d, err := fresh.Confirm(ctx, c, draft.Reference, "protected-one-use")
			if err != nil {
				t.Error(err)
				return
			}
			if d.Dispatch {
				dispatched.Add(1)
				if d.Permit == "" {
					t.Error("missing permit")
				}
			} else if d.Permit != "" {
				t.Error("permit disclosed twice")
			}
		}()
	}
	wg.Wait()
	if dispatched.Load() != 1 || a.calls.Load() != 1 {
		t.Fatalf("dispatch=%d approval=%d", dispatched.Load(), a.calls.Load())
	}
	fresh, _ := New(s.config)
	read, err := fresh.Get(ctx, c, draft.Reference)
	if err != nil || read.State != "unknown" || read.LastPhase != "accepted" {
		t.Fatalf("durable admission: %+v %v", read, err)
	}
	other, _ := fresh.Draft(ctx, c)
	if _, err = fresh.Confirm(ctx, c, other.Reference, "protected-one-use"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second operation bypassed unresolved admission: %v", err)
	}
}

func TestPreviewBindingPolicyRevocationAndFreshRole(t *testing.T) {
	s, a := fixture(t)
	ctx := context.Background()
	c := caller(t, "operator", true)
	work := make([]Work, MaxWorkSample+3)
	for i := range work {
		work[i] = Work{Kind: "transfer", Reference: fmt.Sprint(i)}
	}
	s.config.ActiveWork = func(context.Context) ([]Work, error) { return work, nil }
	d, _ := s.Draft(ctx, c)
	p, err := s.Preview(ctx, c, d.Reference)
	if err != nil || len(p.Work) != MaxWorkSample || !p.WorkTruncated || len(p.Interruptions) != 4 {
		t.Fatalf("preview: %+v %v", p, err)
	}
	a.digest = p.Digest
	work[0].Reference = "changed"
	if _, err = s.Confirm(ctx, c, d.Reference, "protected-one-use"); !errors.Is(err, ErrRefused) {
		t.Fatal("accepted stale preview", err)
	}
	if got, err := s.Get(ctx, c, d.Reference); err != nil || got.State != "refused" {
		t.Fatal("refusal not retained", got, err)
	}
	a.digest = ""
	if _, err = s.Confirm(ctx, caller(t, "operator", false), d.Reference, "protected-one-use"); err == nil {
		t.Fatal("stale administrator role accepted")
	}
	if _, err = s.Get(ctx, caller(t, "another-admin", true), d.Reference); !errors.Is(err, ErrUnknown) {
		t.Fatal("foreign reference disclosed", err)
	}
	if err = os.WriteFile(s.config.PolicyPath, []byte(`{"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, c, d.Reference, "protected-one-use"); !errors.Is(err, ErrRefused) {
		t.Fatal("changed policy admitted", err)
	}
	if a.calls.Load() != 0 {
		t.Fatal("approval consumed for refused admission")
	}
}

func nativeSuccess() Observation {
	return Observation{Bulk: json.RawMessage(`{"success":true,"error":{"code":0},"results":[{"package":"Example","version":"1.0.0-0001","action":"stop","stage":"stopped","finished":true,"success":true},{"package":"Example","version":"1.0.0-0001","action":"start","stage":"started","finished":true,"success":true}]}`), Current: json.RawMessage(`{"id":"Example","version":"1.0.0-0001","additional":{"status":"running"}}`)}
}

func TestNativeResultsPersistAndMissingReplyNeverBecomesSuccess(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	c := caller(t, "operator", true)
	draft, _ := s.Draft(ctx, c)
	d, err := s.Confirm(ctx, c, draft.Reference, "protected-one-use")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Progress(ctx, c, draft.Reference, d.Permit, "stopping"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Progress(ctx, c, draft.Reference, d.Permit, "starting"); err != nil {
		t.Fatal(err)
	}
	obs := nativeSuccess()
	missing := obs
	missing.Bulk = nil
	op, err := s.Record(ctx, c, draft.Reference, d.Permit, missing)
	if err != nil || op.State != "unknown" {
		t.Fatalf("running alone: %+v %v", op, err)
	}
	if _, err = s.Record(ctx, c, draft.Reference, "0000000000000000000000000000000000000000000000000000000000000000", obs); err == nil {
		t.Fatal("foreign permit accepted")
	}
	op, err = s.Record(ctx, c, draft.Reference, d.Permit, obs)
	if err != nil || op.State != "confirmed-running" {
		t.Fatalf("result: %+v %v", op, err)
	}
	// The originating browser is gone; a new Service still reads the result.
	fresh, _ := New(s.config)
	got, err := fresh.Get(ctx, c, draft.Reference)
	if err != nil || got.State != "confirmed-running" {
		t.Fatalf("readback: %+v %v", got, err)
	}
	got, err = fresh.Record(ctx, c, draft.Reference, d.Permit, missing)
	if err != nil || got.State != "confirmed-running" {
		t.Fatal("terminal result was overwritten", got, err)
	}
	// Neither policy nor journal stores the one-use approval or native permit.
	b, _ := os.ReadFile(filepath.Join(s.config.StateDir, "operations.json"))
	if bytes.Contains(b, []byte(d.Permit)) || bytes.Contains(b, []byte("protected-one-use")) {
		t.Fatal("raw secret retained")
	}
}

func TestObservationRejectsWrongTupleOrderIncompleteAndFailures(t *testing.T) {
	policy := Policy{Package: "Example", Version: "1.0.0-0001"}
	for _, tc := range []struct{ name, from, to, want string }{
		{"foreign-package", `"package":"Example"`, `"package":"Other"`, "unknown"},
		{"foreign-version", `"version":"1.0.0-0001"`, `"version":"2.0.0-0001"`, "unknown"},
		{"wrong-order", `"action":"stop"`, `"action":"start"`, "unknown"},
		{"not-finished", `"finished":true`, `"finished":false`, "unknown"},
		{"stop-failure", `"success":true`, `"success":false`, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := nativeSuccess()
			if tc.name == "stop-failure" {
				o.Bulk = bytes.Replace(o.Bulk, []byte(`"finished":true,"success":true`), []byte(`"finished":true,"success":false`), 1)
			} else {
				o.Bulk = bytes.Replace(o.Bulk, []byte(tc.from), []byte(tc.to), 1)
			}
			got, _ := classify(policy, o)
			if got != tc.want {
				t.Fatal(got)
			}
		})
	}
	startFail := nativeSuccess()
	startFail.Bulk = bytes.Replace(startFail.Bulk, []byte(`"stage":"started","finished":true,"success":true`), []byte(`"stage":"started","finished":true,"success":false`), 1)
	if state, reason := classify(policy, startFail); state != "failed" || reason != "start-failed" {
		t.Fatal(state, reason)
	}
}

func TestRoutesRefuseCallersAndCallerSelectedTargets(t *testing.T) {
	s, _ := fixture(t)
	for _, tc := range []struct {
		admin  bool
		params string
		code   string
	}{
		{false, `{}`, "admin_required"},
		{true, `{"package":"Other"}`, "bad_request"},
		{true, `{"executable":"command"}`, "bad_request"},
	} {
		srv, err := api.NewServerWithPermissions(auth{"operator", tc.admin}, []byte(`{"schema":1,"default":"administrators","methods":{"package.restart.draft":"authenticated"}}`), s.Routes()...)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "http://example.test/cgi", bytes.NewBufferString(`{"method":"package.restart.draft","params":`+tc.params+`}`))
		req.Header.Set("Origin", "http://example.test")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(w.Body.Bytes(), &env) != nil || env.Error.Code != tc.code {
			t.Fatalf("%s", w.Body.String())
		}
	}
}

func TestPrivateJournalRejectsSymlinkAndPublicDirectory(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	c := caller(t, "operator", true)
	if err := os.Mkdir(s.config.StateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Draft(ctx, c); !errors.Is(err, ErrRefused) {
		t.Fatal("public state accepted", err)
	}
	os.Chmod(s.config.StateDir, 0700)
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte(`{}`), 0600)
	if err := os.Symlink(outside, filepath.Join(s.config.StateDir, "operations.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Draft(ctx, c); err == nil {
		t.Fatal("symlink journal accepted")
	}
}

func TestUnknownNeedsSeparateProtectedAcknowledgementBeforeNewRestart(t *testing.T) {
	s, a := fixture(t)
	ctx := context.Background()
	c := caller(t, "operator", true)
	draft, _ := s.Draft(ctx, c)
	d, err := s.Confirm(ctx, c, draft.Reference, "protected-one-use")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Progress(ctx, c, draft.Reference, d.Permit, "unknown"); err != nil {
		t.Fatal(err)
	}
	p, err := s.ReconciliationPreview(ctx, c, draft.Reference)
	if err != nil || p.Action != "acknowledge-unknown" || len(p.Interruptions) != 0 {
		t.Fatal(p, err)
	}
	restartPreview, _ := s.Preview(ctx, c, draft.Reference)
	a.digest = restartPreview.Digest
	if _, err = s.AcknowledgeUnknown(ctx, c, draft.Reference, "protected-one-use"); !errors.Is(err, ErrRefused) {
		t.Fatal("restart approval acknowledged uncertainty", err)
	}
	a.digest = p.Digest
	op, err := s.AcknowledgeUnknown(ctx, c, draft.Reference, "protected-one-use")
	if err != nil || op.State != "unknown" || op.Reason != "operator-acknowledged" {
		t.Fatal(op, err)
	}
	repeat, err := s.Confirm(ctx, c, draft.Reference, "protected-one-use")
	if err != nil || repeat.Dispatch || repeat.Permit != "" {
		t.Fatal("acknowledgement reissued permit", repeat, err)
	}
	a.digest = ""
	next, _ := s.Draft(ctx, c)
	if d, err := s.Confirm(ctx, c, next.Reference, "protected-one-use"); err != nil || !d.Dispatch {
		t.Fatal("new deliberate operation blocked", d, err)
	}
}

func TestLockWaitHonorsRequestCancellation(t *testing.T) {
	s, _ := fixture(t)
	c := caller(t, "operator", true)
	if _, err := s.Draft(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(s.config.StateDir, "lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = s.Draft(ctx, c); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lock wait ignored cancellation", err)
	}
}
