package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
)

func TestRequestSlotsAreSharedAcrossSessionsForOneUser(t *testing.T) {
	e, f, _ := setup(t)
	var releases []func()
	for i := 0; i < 4; i++ {
		_, release, code := e.admitRequest(context.Background(), Request{Auth: Auth{Cookie: fmt.Sprintf("session-%d", i), SynoToken: "token"}})
		if code != "" {
			t.Fatal(code)
		}
		releases = append(releases, release)
	}
	if _, release, code := e.admitRequest(context.Background(), Request{Auth: Auth{Cookie: "another-session", SynoToken: "token"}}); code != "EXECUTOR_PAUSED" {
		if release != nil {
			release()
		}
		t.Fatal("user monopolized slots", code)
	}
	f.user.Username = "bob"
	f.user.UID++
	_, release, code := e.admitRequest(context.Background(), Request{Auth: Auth{Cookie: "bob-session", SynoToken: "token"}})
	if code != "" {
		t.Fatal("another user starved", code)
	}
	release()
	for _, release := range releases {
		release()
	}
	_, release, code = e.admitRequest(context.Background(), Request{Auth: Auth{Cookie: "new", SynoToken: "token"}})
	if code != "" {
		t.Fatal("slots not released", code)
	}
	release()
}

func TestOfflineRequestsUseProtectedGrantOwner(t *testing.T) {
	e, _, _ := setup(t)
	id := create(t, e)
	var releases []func()
	for i := 0; i < 4; i++ {
		_, release, code := e.admitRequest(context.Background(), Request{Params: []byte(`{"delegation_id":"` + id + `"}`)})
		if code != "" {
			t.Fatal(code)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()
	if _, release, code := e.admitRequest(context.Background(), Request{Auth: Auth{Cookie: "session", SynoToken: "token"}}); code != "EXECUTOR_PAUSED" {
		if release != nil {
			release()
		}
		t.Fatal("offline and live work used separate quotas", code)
	}
}

func TestRequestAdmissionHasGlobalBoundAndDoesNotCacheFailure(t *testing.T) {
	e, f, _ := setup(t)
	releases := []func(){}
	for i := 0; i < 16; i++ {
		f.user.UID = 2000 + i
		f.user.Username = fmt.Sprint("user", i)
		_, release, code := e.admitRequest(context.Background(), Request{Auth: Auth{Cookie: "session", SynoToken: "token"}})
		if code != "" {
			t.Fatal(code)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()
	f.user.UID++
	if _, release, code := e.admitRequest(context.Background(), Request{Auth: Auth{Cookie: "session", SynoToken: "token"}}); code != "EXECUTOR_PAUSED" {
		if release != nil {
			release()
		}
		t.Fatal(code)
	}
}

// Exercise the public connection path too: admission must be in the transport,
// including batches, rather than just a helper that callers can bypass.
func TestConnectionAppliesOwnerAdmission(t *testing.T) {
	e, f, _ := setup(t)
	releases := []func(){}
	for i := 0; i < 4; i++ {
		_, release, code := e.admitRequest(context.Background(), Request{Auth: Auth{Cookie: "session", SynoToken: "token"}})
		if code != "" {
			t.Fatal(code)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()
	call := func(op string, params string) Response {
		server, client := net.Pipe()
		defer client.Close()
		go func() {
			defer server.Close()
			e.Connection(context.Background(), server, 100, func(net.Conn) (int, error) { return 100, nil })
		}()
		if err := json.NewEncoder(client).Encode(Request{Version: 1, Op: op, Auth: Auth{Cookie: "session", SynoToken: "token"}, Params: json.RawMessage(params)}); err != nil {
			t.Fatal(err)
		}
		var out Response
		if err := json.NewDecoder(client).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, req := range []struct{ op, params string }{{"status", "{}"}, {"root.batch", `{"v":1,"timeout_ms":1000}`}} {
		if out := call(req.op, req.params); out.Code != "EXECUTOR_PAUSED" {
			t.Fatal("connection bypassed quota", out)
		}
	}
	f.user.Username = "bob"
	f.user.UID++
	if out := call("status", "{}"); !out.OK {
		t.Fatal("other user starved", out)
	}
}
