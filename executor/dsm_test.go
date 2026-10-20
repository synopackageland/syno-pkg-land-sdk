package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"testing"
)

type accountsStub struct {
	s   Subject
	err error
}

func (a accountsStub) Lookup(string) (Subject, error) { return a.s, a.err }

type cmdStub struct {
	reply string
	args  []string
	path  string
	err   error
}

func TestDSMCommandDeadlineStaysDistinctFromDenial(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, errors.New("explicit DSM refusal")} {
		d := &liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: &cmdStub{err: cause}, Accounts: accountsStub{s: Subject{Username: "alice", UID: 1200, GID: 100, Groups: []int{100}}}}
		_, err := d.ScopeRoot(context.Background(), Subject{Username: "alice"}, "shared:reports")
		if errors.Is(err, context.DeadlineExceeded) != errors.Is(cause, context.DeadlineExceeded) {
			t.Fatal("lost deadline identity", err)
		}
		e := &Executor{DSM: d, Policy: Policy{App: "app"}}
		_, code := e.eligible(context.Background(), "alice", 1200, true)
		want := "INELIGIBLE"
		if errors.Is(cause, context.DeadlineExceeded) {
			want = "EXECUTOR_PAUSED"
		}
		if code != want {
			t.Fatal(code, want)
		}
	}
}

func (c *cmdStub) Exec(ctx context.Context, p string, args []string) ([]byte, error) {
	c.path = p
	c.args = args
	return []byte(c.reply), c.err
}
func TestDSMAccountFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		pass        bool
	}{
		{"valid", `{"success":true,"data":{"users":[{"name":"alice","uid":1200,"expired":"normal"}]}}`, true},
		{"success empty", `{"success":true,"data":{}}`, false},
		{"success no user", `{"success":true,"data":{"users":[]}}`, false},
		{"missing expiry", `{"success":true,"data":{"users":[{"name":"alice","uid":1200}]}}`, false},
		{"disabled", `{"success":true,"data":{"users":[{"name":"alice","uid":1200,"expired":"now"}]}}`, false},
		{"uid mismatch", `{"success":true,"data":{"users":[{"name":"alice","uid":1201,"expired":"normal"}]}}`, false},
		{"name mismatch", `{"success":true,"data":{"users":[{"name":"bob","uid":1200,"expired":"normal"}]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &cmdStub{reply: tc.reply}
			d := &liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: c, Accounts: accountsStub{s: Subject{Username: "alice", UID: 1200}}}
			_, err := d.Account(context.Background(), "alice")
			if (err == nil) != tc.pass {
				t.Fatal(err)
			}
		})
	}
}

// nativeStub answers SLIBAppPrivUserHas and records each question.
type nativeStub struct {
	allow bool
	err   error
	asked [][3]string
}

func (n *nativeStub) check(_ context.Context, principal, app, addr string) (bool, error) {
	n.asked = append(n.asked, [3]string{principal, app, addr})
	return n.allow, n.err
}

// DSM's native SLIBAppPrivUserHas decides, for the caller's address; the
// AppPriv Web API, which reads every package's UI files, is never called.
func TestDSMAppPrivilegeAsksDSMNativelyForTheClientAddress(t *testing.T) {
	c, n := &cmdStub{}, &nativeStub{allow: true}
	d := &liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: c, AppPriv: n.check}
	ctx := withClientAddress(context.Background(), "192.0.2.1")
	if err := d.Allow(ctx, Subject{Username: "alice"}, "app", "app-privilege"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(n.asked, [][3]string{{"alice", "app", "192.0.2.1"}}) || c.path != "" {
		t.Fatal(n.asked, c.path, c.args)
	}
}

// Administrators bypass App Privileges, including unregistered admin-only Apps.
func TestDSMAppPrivilegeByEntryMode(t *testing.T) {
	user, admin := Subject{Username: "alice"}, Subject{Username: "root-admin", Admin: true}
	for _, tc := range []struct {
		name, entry string
		s           Subject
		allow, pass bool
	}{
		{"no entry mode allowed", "", user, true, true},
		{"no entry mode refused", "", user, false, false},
		{"app-privilege allowed", "app-privilege", user, true, true},
		{"app-privilege refused", "app-privilege", user, false, false},
		{"app-privilege admin", "app-privilege", admin, true, true},
		{"admin ignores App Privileges refusal", "app-privilege", admin, false, true},
		{"admin-only user allowed by DSM", "admin-only", user, true, false},
		{"admin-only admin", "admin-only", admin, true, true},
		{"admin-only admin unregistered", "admin-only", admin, false, true},
		{"unknown mode", "everyone", user, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := &nativeStub{allow: tc.allow}
			d := &liveDSM{AppPriv: n.check}
			ctx := withClientAddress(context.Background(), "192.0.2.1")
			if err := d.Allow(ctx, tc.s, "app", tc.entry); (err == nil) != tc.pass {
				t.Fatal(err)
			}
			wantCalls := 0
			if tc.entry != "everyone" && tc.entry != "admin-only" && !tc.s.Admin {
				wantCalls = 1
			}
			if len(n.asked) != wantCalls {
				t.Fatal("DSM asked", n.asked)
			}
		})
	}
}

func TestDSMAppPrivilegeRefusesWithoutAUsableAddressOrAnswer(t *testing.T) {
	for _, addr := range []string{"", "localhost", "192.0.2.1:5000", "192.000.2.1", "::FFFF:192.0.2.1", "fe80::1%eth0", " 192.0.2.1"} {
		n := &nativeStub{allow: true}
		d := &liveDSM{AppPriv: n.check}
		if d.Allow(withClientAddress(context.Background(), addr), Subject{Username: "alice"}, "app", "") == nil || len(n.asked) != 0 {
			t.Fatalf("address %q: %v", addr, n.asked)
		}
	}
	n := &nativeStub{allow: true}
	if (&liveDSM{AppPriv: n.check}).Allow(context.Background(), Subject{Username: "alice"}, "app", "") == nil || len(n.asked) != 0 {
		t.Fatal("no address allowed")
	}
	ctx := withClientAddress(context.Background(), "192.0.2.1")
	if (&liveDSM{}).Allow(ctx, Subject{Username: "alice"}, "app", "") == nil {
		t.Fatal("no native check allowed")
	}
	if (&liveDSM{AppPriv: n.check}).Allow(ctx, Subject{Username: "alice"}, "", "") == nil {
		t.Fatal("empty app allowed")
	}
	if (&liveDSM{AppPriv: (&nativeStub{allow: true, err: errors.New("bridge")}).check}).Allow(ctx, Subject{Username: "alice"}, "app", "") == nil {
		t.Fatal("bridge failure allowed")
	}
}

func TestDSMAppPrivilegeKeepsTheCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(withClientAddress(context.Background(), "192.0.2.1"))
	cancel()
	d := &liveDSM{AppPriv: (&nativeStub{err: errors.New("killed")}).check}
	if err := d.Allow(ctx, Subject{Username: "alice"}, "app", ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// DSM 7.1 with skip_ip_checking=no invalidates an otherwise live session
// when it is replayed to entry.cgi from 127.0.0.1. Verify must ask the native
// authenticator with the original browser address and the required token.
func TestDSMVerifyPreservesIPBoundTokenSession(t *testing.T) {
	for _, addr := range []string{"192.0.2.7", "2001:db8::7"} {
		t.Run(addr, func(t *testing.T) {
			called := false
			d := &liveDSM{session: func(ctx context.Context, path string, env []string) ([]byte, error) {
				called = true
				want := []string{"REQUEST_METHOD=GET", "HTTP_COOKIE=id=session", "HTTP_X_SYNO_TOKEN=tok+en/=", "QUERY_STRING=SynoToken=tok%2Ben%2F%3D", "REMOTE_ADDR=" + addr}
				if path != "/usr/syno/synoman/webman/modules/authenticate.cgi" || !reflect.DeepEqual(env, want) {
					t.Errorf("unexpected authentication carriers: path=%q", path)
				}
				return []byte("alice\n"), nil
			}}
			name, err := d.Verify(withClientAddress(context.Background(), addr), "session", "tok+en/=")
			if err != nil || name != "alice" || !called {
				t.Fatalf("IP-bound token session refused: user=%q err=%v native=%v", name, err, called)
			}
		})
	}
}
func TestDSMArgvAndEmbeddedError(t *testing.T) {
	c := &cmdStub{reply: `{"success":true,"data":{"files":[{"error":{"code":407}}]}}`}
	d := &liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: c}
	_, err := d.API(context.Background(), "alice", "files.createFolder", map[string]any{"path": "/home", "name": "$(touch nope);`x`"})
	if err == nil || c.path != "/usr/syno/bin/synowebapi" || !contains(c.args, "runner=alice") {
		t.Fatal(err, c)
	}
	if !contains(c.args, `name="$(touch nope);`+"`x`"+`"`) {
		t.Fatal(c.args)
	}
	c.err = errors.New("unavailable")
	if _, err = d.API(context.Background(), "alice", "files.list", map[string]any{"path": "/home"}); err == nil {
		t.Fatal("command error")
	}
}

func TestSubdirectoryBinding(t *testing.T) {
	e, f, _ := setup(t)
	f.root = t.TempDir()
	f.root = realTestPath(t, f.root)
	if err := makeTestDir(f.root + "/downloads"); err != nil {
		t.Fatal(err)
	}
	out := request(e, "delegate.create", map[string]any{"job_id": "j", "ops": []string{"files.list"}, "scope": "home", "path": "/home/downloads", "ttl_seconds": 60})
	if !out.OK {
		t.Fatal(out)
	}
}

func TestDSMEmptyOperationDataDenied(t *testing.T) {
	for _, op := range []string{"files.list", "files.info", "files.createFolder", "files.checkPermission"} {
		c := &cmdStub{reply: `{"success":true,"data":{}}`}
		d := &liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: c}
		if _, err := d.API(context.Background(), "alice", op, map[string]any{"path": "/home", "name": "new"}); err == nil {
			t.Fatal(op, "empty success accepted")
		}
	}
}

type eligibilityCommand struct{ expired string }

func (c eligibilityCommand) Exec(context.Context, string, []string) ([]byte, error) {
	return []byte(`{"success":true,"data":{"users":[{"name":"alice","uid":1200,"expired":"` + c.expired + `"}]}}`), nil
}
func TestDefaultAppPrivilegeNeverBypassesExplicitDenialOrDisabledAccount(t *testing.T) {
	for _, tc := range []struct{ name, expiry, deny, want string }{
		{"ordinary defaults", "normal", "", ""},
		{"App refused", "normal", "app", "APP_DENIED"},
		{"File Station refused", "normal", "SYNO.SDS.App.FileStation3.Instance", "APP_DENIED"},
		{"disabled with default Apps", "now", "", "INELIGIBLE"},
		{"unknown account state", "", "", "INELIGIBLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := setup(t)
			native := func(_ context.Context, _, app, _ string) (bool, error) { return app != tc.deny, nil }
			e.DSM = &liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: eligibilityCommand{expired: tc.expiry}, AppPriv: native, Accounts: accountsStub{s: Subject{Username: "alice", UID: 1200, GID: 100, Groups: []int{100}}}}
			_, code := e.eligible(withClientAddress(context.Background(), localAddress), "alice", 1200, true)
			if code != tc.want {
				t.Fatal(code, tc.want)
			}
		})
	}
}

func TestDSMSharedScopeUsesMeasuredGetVolumeContract(t *testing.T) {
	volume := t.TempDir()
	volume, err := filepath.EvalSymlinks(volume)
	if err != nil {
		t.Fatal(err)
	}
	share := filepath.Join(volume, "reports")
	if err := os.Mkdir(share, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, data string
		pass       bool
	}{
		{"DSM single share", `{"name":"reports","vol_path":` + strconv.Quote(volume) + `,"desc":"test","uuid":"fixture"}`, true},
		{"different share", `{"name":"other","vol_path":` + strconv.Quote(volume) + `}`, false},
		{"missing volume", `{"name":"reports"}`, false},
		{"relative volume", `{"name":"reports","vol_path":"relative"}`, false},
		{"unclean volume", `{"name":"reports","vol_path":"/volume1/../etc"}`, false},
		{"root volume", `{"name":"reports","vol_path":"/"}`, false},
		{"null", `null`, false},
		{"invented list contract", `{"shares":[{"name":"reports","path":` + strconv.Quote(share) + `}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cmdStub{reply: `{"success":true,"data":` + tc.data + `}`}
			d := &liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: cmd}
			root, err := d.ScopeRoot(context.Background(), Subject{}, "shared:reports")
			if (err == nil) != tc.pass || tc.pass && root != share {
				t.Fatalf("root=%q err=%v", root, err)
			}
			if !reflect.DeepEqual(cmd.args, []string{"--exec", "api=SYNO.Core.Share", "method=get", "version=1", `name="reports"`}) {
				t.Fatal(cmd.args)
			}
		})
	}
}

func TestListAndInfoAskForTheSize(t *testing.T) {
	for _, op := range []string{"files.list", "files.info"} {
		c := &cmdStub{reply: `{"success":true,"data":{"files":[{"path":"/home/a","name":"a","isdir":false,"additional":{"size":3}}]}}`}
		d := &liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: c}
		if _, err := d.API(context.Background(), "alice", op, map[string]any{"path": "/home/a"}); err != nil || !contains(c.args, `additional=["size"]`) {
			t.Fatal(op, err, c.args)
		}
	}
}

func TestCheckPermissionProbesWithAFreshName(t *testing.T) {
	probe := regexp.MustCompile(`^filename="\.synopkgland-permission-probe-[0-9a-f]{32}"$`)
	seen := map[string]bool{}
	for range 2 {
		c := &cmdStub{reply: `{"success":true,"data":{"blSkip":false}}`}
		if _, err := (&liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: c}).API(context.Background(), "alice", "files.checkPermission", map[string]any{"path": "/home"}); err != nil {
			t.Fatal(err)
		}
		for _, a := range c.args {
			if probe.MatchString(a) {
				seen[a] = true
			}
		}
	}
	if len(seen) != 2 {
		t.Fatal("probe name missing or repeated", seen)
	}
}
