package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeDSM struct {
	apiMu                    sync.Mutex
	user                     Subject
	accountErr, privilegeErr error
	apiCalls, accountCalls   int
	root                     string
	// apiReply, when set, answers API calls; lastOp and lastArgs record them.
	apiReply func(op string, args map[string]any) (json.RawMessage, error)
	// permissionErr, when set, refuses files.checkPermission.
	permissionErr error
	lastOp        string
	lastArgs      map[string]any
	// allowedAddr, when set, is the only client address App Privileges allow.
	allowedAddr string
}

func (f *fakeDSM) Verify(context.Context, string, string) (string, error) {
	return f.user.Username, nil
}
func (f *fakeDSM) Account(context.Context, string) (Subject, error) {
	f.apiMu.Lock()
	f.accountCalls++
	f.apiMu.Unlock()
	return f.user, f.accountErr
}
func (f *fakeDSM) Allow(ctx context.Context, _ Subject, _, _ string) error {
	f.apiMu.Lock()
	only := f.allowedAddr
	f.apiMu.Unlock()
	if addr, _ := clientAddress(ctx); only != "" && addr != only {
		return errors.New("address")
	}
	return f.privilegeErr
}
func (f *fakeDSM) ScopeRoot(context.Context, Subject, string) (string, error) { return f.root, nil }
func (f *fakeDSM) API(_ context.Context, _ string, op string, args map[string]any) (json.RawMessage, error) {
	f.apiMu.Lock()
	f.apiCalls++
	f.lastOp, f.lastArgs = op, args
	reply, denied := f.apiReply, f.permissionErr
	f.apiMu.Unlock()
	if op == "files.checkPermission" && denied != nil {
		return nil, denied
	}
	if reply != nil && op != "files.checkPermission" {
		return reply(op, args)
	}
	return json.RawMessage(`{"allowed":true}`), nil
}

type fakeWriter struct{ calls int }

func (f *fakeWriter) Write(context.Context, Subject, string, string, string, string) (MoveReceipt, error) {
	f.calls++
	return MoveReceipt{}, nil
}
func setup(t *testing.T) (*Executor, *fakeDSM, *fakeWriter) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f := &fakeDSM{user: Subject{Username: "alice", UID: 1200, GID: 100, Groups: []int{100}, Home: root, Expired: "normal"}, root: root}
	w := &fakeWriter{}
	e := &Executor{Policy: Policy{Version: 1, Package: "p2probe", App: "app", Scope: "home", Operations: allOps}, DSM: f, Writer: w, Store: &Store{Dir: t.TempDir()}, Now: func() time.Time { return time.Unix(1000, 0) }}
	return e, f, w
}
func request(e *Executor, op string, params any) Response {
	b, _ := json.Marshal(params)
	return e.Handle(context.Background(), Request{Version: 1, Op: op, Auth: Auth{Cookie: "session", SynoToken: "token"}, Params: b})
}
func create(t *testing.T, e *Executor) string {
	t.Helper()
	r := request(e, "delegate.create", map[string]any{"job_id": "job1", "ops": []string{"files.write", "files.list"}, "scope": "home", "path": "/home", "ttl_seconds": 60})
	if !r.OK {
		t.Fatalf("create: %+v", r)
	}
	b, _ := json.Marshal(r.Result)
	var d Delegation
	json.Unmarshal(b, &d)
	return d.ID
}
func TestRejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Executor, *fakeDSM, *fakeWriter, string)
		params map[string]any
		code   string
	}{
		{"missing account", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) { f.accountErr = errors.New("missing") }, nil, "INELIGIBLE"},
		{"disabled", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) { f.user.Expired = "now" }, nil, "INELIGIBLE"},
		{"unknown disabled state", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) { f.user.Expired = "" }, nil, "INELIGIBLE"},
		{"system runner", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) { f.user.UID = 1 }, nil, "INELIGIBLE"},
		{"uid reuse", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) { f.user.UID++ }, nil, "INELIGIBLE"},
		{"app deny", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) { f.privilegeErr = errors.New("deny") }, nil, "APP_DENIED"},
		{"expired", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) {
			e.Now = func() time.Time { return time.Unix(1060, 0) }
		}, nil, "DELEGATION_DENIED"},
		{"revoked", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) {
			d, _ := e.Store.Get(id)
			d.Revoked = true
			e.Store.Put(d)
		}, nil, "DELEGATION_DENIED"},
		{"job", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) {}, map[string]any{"job_id": "job2"}, "DELEGATION_DENIED"},
		{"scope", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) {}, map[string]any{"dest_dir": "/etc"}, "SCOPE_DENIED"},
		{"dotdot", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) {}, map[string]any{"dest_dir": "/home/../home"}, "INVALID_REQUEST"},
		{"name", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) {}, map[string]any{"name": "../oops"}, "INVALID_REQUEST"},
		{"outside staging", func(e *Executor, f *fakeDSM, w *fakeWriter, id string) {}, map[string]any{"staging": "../secret"}, "INVALID_REQUEST"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, f, w := setup(t)
			id := create(t, e)
			tc.mutate(e, f, w, id)
			p := map[string]any{"delegation_id": id, "job_id": "job1", "staging": "a", "dest_dir": "/home", "name": "new"}
			for k, v := range tc.params {
				p[k] = v
			}
			r := request(e, "files.write", p)
			if r.OK || r.Code != tc.code || w.calls != 0 {
				t.Fatalf("%+v calls=%d", r, w.calls)
			}
		})
	}
}
func TestUnknownAndPolicy(t *testing.T) {
	e, _, _ := setup(t)
	if r := request(e, "shell", nil); r.Code != "UNKNOWN_OP" {
		t.Fatal(r)
	}
	e.Policy.Operations = []string{"status"}
	if r := request(e, "delegate.create", nil); r.Code != "POLICY_DENIED" {
		t.Fatal(r)
	}
}
func TestSpoofedUsername(t *testing.T) {
	e, _, _ := setup(t)
	r := request(e, "delegate.create", map[string]any{"username": "bob", "job_id": "j", "ops": []string{"files.list"}, "scope": "home", "path": "/home", "ttl_seconds": 10})
	if r.OK || r.Code != "INVALID_REQUEST" {
		t.Fatal(r)
	}
	id := create(t, e)
	d, _ := e.Store.Get(id)
	if d.Subject.Username != "alice" {
		t.Fatal(d)
	}
}
func TestScopeSymlink(t *testing.T) {
	e, f, _ := setup(t)
	id := create(t, e)
	os.Symlink(t.TempDir(), filepath.Join(f.root, "escape"))
	r := request(e, "files.list", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/escape"})
	if r.Code != "SCOPE_DENIED" || f.apiCalls != 1 {
		t.Fatal(r, f.apiCalls)
	}
}
func TestOperationDelegation(t *testing.T) {
	e, _, _ := setup(t)
	id := create(t, e)
	r := request(e, "files.info", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home"})
	if r.Code != "DELEGATION_DENIED" {
		t.Fatal(r)
	}
}
func TestListAndRevokeOwnership(t *testing.T) {
	e, f, _ := setup(t)
	id := create(t, e)
	f.user.Username = "bob"
	r := request(e, "delegate.list", map[string]any{})
	b, _ := json.Marshal(r.Result)
	if strings.Contains(string(b), id) {
		t.Fatal(r)
	}
	r = request(e, "delegate.revoke", map[string]any{"delegation_id": id})
	if r.Code != "DELEGATION_DENIED" {
		t.Fatal(r)
	}
	r = e.Handle(context.Background(), Request{Version: 1, Op: "delegate.revoke", Params: json.RawMessage(`{"delegation_id":"` + id + `","job_id":"job1"}`)})
	if !r.OK {
		t.Fatal(r)
	}
}
func TestProtocol(t *testing.T) {
	for _, tc := range []struct{ name, raw, code string }{{"oversize", strings.Repeat("x", 65537) + "\n", "REQUEST_TOO_LARGE"}, {"unknown field", `{"v":1,"op":"status","username":"admin"}` + "\n", "INVALID_REQUEST"}, {"duplicate", `{"v":1,"v":1,"op":"status"}` + "\n", "INVALID_REQUEST"}, {"utf8", string([]byte{255}) + "\n", "INVALID_REQUEST"}, {"version", `{"v":2,"op":"status"}` + "\n", "INVALID_REQUEST"}} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := setup(t)
			a, b := net.Pipe()
			go func() {
				defer a.Close()
				e.Connection(context.Background(), a, 100, func(net.Conn) (int, error) { return 100, nil })
			}()
			defer b.Close()
			go b.Write([]byte(tc.raw))
			var r Response
			if err := json.NewDecoder(b).Decode(&r); err != nil || r.Code != tc.code {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}
func TestWrongPeer(t *testing.T) {
	e, _, _ := setup(t)
	a, b := net.Pipe()
	go func() {
		defer a.Close()
		e.Connection(context.Background(), a, 100, func(net.Conn) (int, error) { return 101, nil })
	}()
	defer b.Close()
	var r Response
	if json.NewDecoder(b).Decode(&r) == nil {
		t.Fatal("wrong uid got response")
	}
}
func TestStorePersistence(t *testing.T) {
	e, _, _ := setup(t)
	id := create(t, e)
	d, err := (&Store{Dir: e.Store.Dir}).Get(id)
	if err != nil || d.Subject.UID != 1200 {
		t.Fatal(d, err)
	}
	info, _ := os.Stat(filepath.Join(e.Store.Dir, id+".json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if _, err = e.Store.Get("../x"); err == nil {
		t.Fatal("traversal")
	}
}

func TestFileStationPrivilegeRequiredSeparately(t *testing.T) {
	e, f, _ := setup(t)
	e.DSM = &selectivePrivilege{fakeDSM: f}
	if out := request(e, "delegate.create", map[string]any{"job_id": "j", "ops": []string{"files.list"}, "scope": "home", "path": "/home", "ttl_seconds": 10}); out.Code != "APP_DENIED" {
		t.Fatal(out)
	}
}

type selectivePrivilege struct{ *fakeDSM }

func (f *selectivePrivilege) Allow(_ context.Context, _ Subject, app, _ string) error {
	if app == "SYNO.SDS.App.FileStation3.Instance" {
		return errors.New("deny File Station")
	}
	return nil
}
func TestSharedScope(t *testing.T) {
	e, _, _ := setup(t)
	e.Policy.Scope = "shared:p2probe-share"
	out := request(e, "delegate.create", map[string]any{"job_id": "j", "ops": []string{"files.list"}, "scope": "shared:p2probe-share", "path": "/p2probe-share", "ttl_seconds": 10})
	if !out.OK {
		t.Fatal(out)
	}
}

type entryRecorder struct {
	*fakeDSM
	entries map[string]string
}

func (f *entryRecorder) Allow(_ context.Context, _ Subject, app, entry string) error {
	f.entries[app] = entry
	return nil
}
func TestPackageEntryModeReachesAppPrivilegeOnly(t *testing.T) {
	e, f, _ := setup(t)
	e.Policy.Entry = "app-privilege"
	r := &entryRecorder{fakeDSM: f, entries: map[string]string{}}
	e.DSM = r
	if out := request(e, "delegate.create", map[string]any{"job_id": "j", "ops": []string{"files.list"}, "scope": "home", "path": "/home", "ttl_seconds": 10}); !out.OK {
		t.Fatal(out)
	}
	// File Station is a DSM App without the package's entry mode.
	if r.entries["app"] != "app-privilege" || r.entries["SYNO.SDS.App.FileStation3.Instance"] != "" {
		t.Fatal(r.entries)
	}
}
func TestPolicyValidation(t *testing.T) {
	base := Policy{Version: 1, Package: "p2probe", App: "app", Scope: "home", Operations: allOps, WebAPI: map[string]WebAPIProfile{"calendar": {Purpose: "Calendar", APIs: map[string]map[string][]int{"SYNO.Cal.Event": {"list": {6}}}}}, Portal: &PortalPolicy{Application: "Probe", Alias: "probe"}}
	for _, entry := range []string{"", "admin-only", "app-privilege"} {
		p := base
		p.Entry = entry
		if validatePolicy(p, "p2probe") != nil {
			t.Fatal("valid policy", entry)
		}
	}
	for _, mutate := range []func(*Policy){func(p *Policy) { p.Package = "another" }, func(p *Policy) { p.Version = 2 }, func(p *Policy) { p.Operations = []string{"shell"} }, func(p *Policy) { p.Operations = []string{"status", "status"} }, func(p *Policy) { p.Scope = "../../etc" }, func(p *Policy) { p.Root = rootDigests(map[string]string{"a": "bad"}) }, func(p *Policy) { p.Entry = "everyone" }} {
		p := base
		mutate(&p)
		if validatePolicy(p, "p2probe") == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}
func TestFilenameAndTTL(t *testing.T) {
	for _, n := range []string{"", "..", "a..b", "a/b", "a\x00b", "a\tb", "a\nb"} {
		if validName(n) {
			t.Fatal(n)
		}
	}
	e, _, _ := setup(t)
	for _, ttl := range []int{0, -1, 2592001} {
		out := request(e, "delegate.create", map[string]any{"job_id": "j", "ops": []string{"files.list"}, "scope": "home", "path": "/home", "ttl_seconds": ttl})
		if out.Code != "INVALID_REQUEST" {
			t.Fatal(out)
		}
	}
}
func TestStoreRejectSymlinkAndMode(t *testing.T) {
	e, _, _ := setup(t)
	id := create(t, e)
	p := filepath.Join(e.Store.Dir, id+".json")
	os.Chmod(p, 0644)
	if _, err := e.Store.Get(id); err == nil {
		t.Fatal("mode")
	}
	os.Remove(p)
	os.Symlink(filepath.Join(t.TempDir(), "secret"), p)
	if _, err := e.Store.Get(id); err == nil {
		t.Fatal("symlink")
	}
}

func TestCallerRequiresBothSessionValuesWithoutCredentialLeak(t *testing.T) {
	e, _, _ := setup(t)
	for _, auth := range []Auth{{Cookie: "secret-cookie"}, {SynoToken: "secret-token"}, {Cookie: "secret-cookie", SynoToken: "secret\r\nInjected: value"}, {Cookie: "secret-cookie", SynoToken: strings.Repeat("x", 4097)}} {
		out := e.Handle(context.Background(), Request{Version: 1, Op: "delegate.list", Auth: auth, Params: json.RawMessage(`{}`)})
		b, _ := json.Marshal(out)
		if out.Code != "AUTH_DENIED" || strings.Contains(string(b), "secret") {
			t.Fatal("invalid credentials not safely refused")
		}
	}
}
