package delegate

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type identity struct{ user string }

func (i identity) Authenticate(*http.Request) (api.Identity, error) {
	return api.Identity{User: i.user}, nil
}
func service(t *testing.T, c *Client) *Service {
	dir := t.TempDir()
	s := &Service{Client: c, Store: background.NewStore(filepath.Join(dir, "background")), Scope: "home", StagingDir: filepath.Join(dir, "executor-staging"), LookupUID: func(string) string { return "1205" }}
	return s
}
func rpc(t *testing.T, s *Service, user, method, params string) map[string]any {
	t.Helper()
	routes := s.Routes()
	server, e := api.NewServerWithPermissions(identity{user}, []byte(`{"schema":1,"default":"administrators","methods":{}}`), routes...)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "http://localhost/api", strings.NewReader(`{"method":"`+method+`","params":`+params+`}`))
	r.Header.Set("Origin", "http://"+r.Host)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "id", Value: "cookie-only"})
	r.Header.Set("X-SYNO-TOKEN", "request-token")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	var out map[string]any
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal(w.Body.String())
	}
	return out
}
func TestCGIForwardsSessionPairNeverUsername(t *testing.T) {
	c := executor(t, func(q Request) string {
		wire, _ := json.Marshal(q)
		if strings.Contains(string(wire), "username") || strings.Contains(string(wire), "alice") {
			t.Errorf("username leaked: %s", wire)
		}
		if q.Auth["dsm_cookie"] != "cookie-only" || q.Auth["dsm_synotoken"] != "request-token" {
			t.Errorf("auth %+v", q.Auth)
		}
		return `{"ok":true,"result":{"delegation_id":"d1","subject":{"username":"alice","uid":1205},"expires_at":"2030-01-01T00:00:00Z"}}` + "\n"
	})
	s := service(t, c)
	for _, method := range []string{"delegate.create", "delegate.list", "delegate.revoke"} {
		params := "{}"
		if method == "delegate.create" {
			params = `{"job_id":"j","ops":["files.write"],"path":"/home/Downloads","ttl_seconds":600}`
		}
		if method == "delegate.revoke" {
			params = `{"delegation_id":"d1"}`
		}
		out := rpc(t, s, "alice", method, params)
		if out["success"] != true {
			t.Fatalf("%s: %+v", method, out)
		}
	}
	out := rpc(t, s, "alice", "delegate.create", `{"job_id":"j","ops":["files.write"],"path":"/home","ttl_seconds":600,"username":"victim"}`)
	if out["success"] != false {
		t.Fatal("accepted username")
	}
}
func TestDownloadJobsFinishCancelOwnerAndRecovery(t *testing.T) {
	var mu sync.Mutex
	revokes := 0
	writes := 0
	c := executor(t, func(q Request) string {
		switch q.Op {
		case "delegate.create":
			return `{"ok":true,"result":{"delegation_id":"d1","subject":{"username":"alice","uid":1205},"expires_at":"2030-01-01T00:00:00Z"}}` + "\n"
		case "delegate.revoke":
			mu.Lock()
			revokes++
			mu.Unlock()
			var p map[string]any
			json.Unmarshal(q.Params, &p)
			if q.Auth == nil && p["job_id"] == nil {
				t.Error("missing job binding")
			}
		case "files.write":
			mu.Lock()
			writes++
			mu.Unlock()
		}
		return `{"ok":true,"result":{}}` + "\n"
	})
	s := service(t, c)
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("download")) }))
	defer web.Close()
	s.HTTP = web.Client()
	submit := func(key string) string {
		out := rpc(t, s, "alice", "delegate.jobs.submit", `{"key":"`+key+`","url":"`+web.URL+`/file","dest":"/home/Downloads","name":"file.txt"}`)
		if out["success"] != true {
			t.Fatal(out)
		}
		return out["data"].(map[string]any)["id"].(string)
	}
	id := submit("first")
	if e := s.RunOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	j, _ := s.Store.Get(context.Background(), id)
	if j.DelegationID != "d1" {
		t.Fatalf("delegation not stored on job: %+v", j)
	}
	if j.State != background.StateSucceeded {
		t.Fatalf("job %+v", j)
	}
	id = submit("second")
	out := rpc(t, s, "bob", "delegate.jobs.cancel", `{"id":"`+id+`"}`)
	if out["success"] != false {
		t.Fatal("other owner canceled")
	}
	out = rpc(t, s, "alice", "delegate.jobs.cancel", `{"id":"`+id+`"}`)
	if out["success"] != true {
		t.Fatal(out)
	}
	mu.Lock()
	if revokes != 2 || writes != 1 {
		t.Errorf("revokes %d writes %d", revokes, writes)
	}
	mu.Unlock()
	id = submit("restart")
	s.Store.Claim(context.Background())
	if e := s.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	j, _ = s.Store.Get(context.Background(), id)
	if j.State != background.StateInterrupted {
		t.Fatal(j)
	}
	if pending, e := s.Store.DelegationCleanup(context.Background()); e != nil || len(pending) != 0 {
		t.Fatalf("cleanup %v %v", pending, e)
	}
}
func TestCapabilityUnavailableAndBlocked(t *testing.T) {
	s := service(t, &Client{Socket: filepath.Join(t.TempDir(), "absent")})
	out := rpc(t, s, "alice", "delegate.status", "{}")
	if out["data"].(map[string]any)["state"] != "needs-admin-approval" {
		t.Fatal(out)
	}
	s.Client = executor(t, func(Request) string { return `{"ok":false,"code":"POLICY_DENIED"}` + "\n" })
	out = rpc(t, s, "alice", "delegate.status", "{}")
	if out["data"].(map[string]any)["state"] != "blocked" {
		t.Fatal(out)
	}
}
func TestDownloadLimitTimeoutAndStagingCleanup(t *testing.T) {
	c := executor(t, func(Request) string { return `{"ok":true,"result":{}}` + "\n" })
	s := service(t, c)
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Length", "67108865")
		w.WriteHeader(200)
	}))
	defer web.Close()
	s.HTTP = web.Client()
	for _, p := range []string{"/big", "/slow"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		e := s.download(ctx, DownloadInput{URL: web.URL + p, Dest: "/home", Name: "file"}, JobContext{Client: c, JobID: "j", DelegationID: "d", StagingDir: s.StagingDir})
		cancel()
		if e == nil {
			t.Fatal("unbounded download")
		}
		files, _ := os.ReadDir(s.StagingDir)
		if len(files) != 0 {
			t.Fatal("staging leaked")
		}
	}
}

func TestRevocationFailureIsDurableAndRetried(t *testing.T) {
	var mu sync.Mutex
	reject := true
	creates := 0
	c := executor(t, func(q Request) string {
		switch q.Op {
		case "delegate.create":
			mu.Lock()
			creates++
			mu.Unlock()
			return `{"ok":true,"result":{"delegation_id":"d1","subject":{"username":"alice","uid":"1205"},"expires_at":"2030-01-01T00:00:00Z"}}` + "\n"
		case "delegate.revoke":
			mu.Lock()
			bad := reject
			mu.Unlock()
			if bad {
				return `{"ok":false,"code":"TEMPORARY_FAILURE"}` + "\n"
			}
		}
		return `{"ok":true,"result":{}}` + "\n"
	})
	s := service(t, c)
	params := `{"key":"same","url":"https://example.com/file","dest":"/home/Downloads","name":"file"}`
	a := rpc(t, s, "alice", "delegate.jobs.submit", params)
	b := rpc(t, s, "alice", "delegate.jobs.submit", params)
	if a["success"] != true || b["success"] != true || a["data"].(map[string]any)["id"] != b["data"].(map[string]any)["id"] {
		t.Fatal(a, b)
	}
	mu.Lock()
	if creates != 1 {
		t.Errorf("duplicate created %d grants", creates)
	}
	mu.Unlock()
	id := a["data"].(map[string]any)["id"].(string)
	out := rpc(t, s, "alice", "delegate.jobs.cancel", `{"id":"`+id+`"}`)
	if out["data"].(map[string]any)["revocation_pending"] != true {
		t.Fatal(out)
	}
	if list, e := s.Store.DelegationCleanup(context.Background()); e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	mu.Lock()
	reject = false
	mu.Unlock()
	// A fresh process can retry using only durable job metadata.
	s.Store = background.NewStore(filepath.Join(filepath.Dir(s.StagingDir), "background"))
	if e := s.Sweep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if list, e := s.Store.DelegationCleanup(context.Background()); e != nil || len(list) != 0 {
		t.Fatal(list, e)
	}
}
func TestRunningJobCancellationStopsDownloadAndRevokes(t *testing.T) {
	var mu sync.Mutex
	revokes := 0
	c := executor(t, func(q Request) string {
		if q.Op == "delegate.create" {
			return `{"ok":true,"result":{"delegation_id":"d1","subject":{"username":"alice","uid":1205},"expires_at":"2030-01-01T00:00:00Z"}}` + "\n"
		}
		if q.Op == "delegate.revoke" {
			mu.Lock()
			revokes++
			mu.Unlock()
		}
		if q.Op == "files.write" {
			t.Error("canceled download wrote destination")
		}
		return `{"ok":true,"result":{}}` + "\n"
	})
	started := make(chan struct{})
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer web.Close()
	s := service(t, c)
	s.HTTP = web.Client()
	out := rpc(t, s, "alice", "delegate.jobs.submit", `{"key":"cancel-running","url":"`+web.URL+`","dest":"/home","name":"file"}`)
	if out["success"] != true {
		t.Fatal(out)
	}
	id := out["data"].(map[string]any)["id"].(string)
	done := make(chan error, 1)
	go func() { done <- s.RunOnce(context.Background()) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	out = rpc(t, s, "alice", "delegate.jobs.cancel", `{"id":"`+id+`"}`)
	if out["success"] != true {
		t.Fatal(out)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not stop worker")
	}
	job, _ := s.Store.Get(context.Background(), id)
	if job.State != background.StateCanceled {
		t.Fatal(job)
	}
	mu.Lock()
	if revokes < 1 {
		t.Error("not revoked")
	}
	mu.Unlock()
}
func TestSessionCookieMustBeSingleAndRequestScoped(t *testing.T) {
	for _, r := range []*http.Request{nil, httptest.NewRequest("POST", "http://localhost", nil)} {
		if _, e := api.DSMSession(r); e == nil {
			t.Fatal("missing cookie accepted")
		}
	}
	r := httptest.NewRequest("POST", "http://localhost", nil)
	r.AddCookie(&http.Cookie{Name: "id", Value: "a"})
	r.AddCookie(&http.Cookie{Name: "id", Value: "b"})
	if _, e := api.DSMSession(r); e == nil {
		t.Fatal("ambiguous cookie accepted")
	}
}

func TestStatusRejectsPreviousExecutorPolicy(t *testing.T) {
	c := executor(t, func(Request) string {
		return `{"ok":true,"result":{"ready":true,"version":"1","executor_digest":"old","policy_digest":"old"}}` + "\n"
	})
	s := service(t, c)
	s.ExecutorDigest, s.PolicyDigest = "current", "current"
	if got := s.Status(context.Background(), nil); got.State != "blocked" {
		t.Fatalf("stale executor ready: %+v", got)
	}
}
