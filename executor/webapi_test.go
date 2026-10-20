package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type webDSM struct {
	*fakeDSM
	runner, api, method string
	version, calls      int
	params              map[string]any
	reply               json.RawMessage
	err                 error
}

func (f *webDSM) WebAPI(ctx context.Context, runner, api, method string, version int, params map[string]any) (json.RawMessage, error) {
	f.calls++
	f.runner, f.api, f.method, f.version, f.params = runner, api, method, version, params
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 11*time.Second {
		return nil, errors.New("missing bounded deadline")
	}
	if f.reply == nil {
		return json.RawMessage(`{"success":true,"data":{"list":[]}}`), f.err
	}
	return f.reply, f.err
}
func webSetup(t *testing.T) (*Executor, *webDSM) {
	e, f, _ := setup(t)
	e.Policy.Operations = []string{"status", "delegate.create", "delegate.list", "delegate.revoke", "delegate.disable", "delegate.enable", "delegate.audit", "webapi.call"}
	e.Policy.Scope = ""
	e.Policy.WebAPI = map[string]WebAPIProfile{"calendar": {Purpose: "Create approved calendar reminders", APIs: map[string]map[string][]int{"SYNO.Cal.Event": {"list": {6}, "create": {6}, "delete": {6}}}}}
	w := &webDSM{fakeDSM: f}
	e.DSM = w
	return e, w
}
func webGrant(t *testing.T, e *Executor, long bool) string {
	t.Helper()
	p := map[string]any{"ops": []string{"webapi.call"}, "scope": "calendar"}
	if long {
		p["lifetime"] = "until-revoked"
	} else {
		p["job_id"] = "job1"
		p["ttl_seconds"] = 60
	}
	r := request(e, "delegate.create", p)
	if !r.OK {
		t.Fatalf("create WebAPI grant: %+v", r)
	}
	b, _ := json.Marshal(r.Result)
	var d Delegation
	json.Unmarshal(b, &d)
	return d.ID
}
func webCall(e *Executor, id string, long bool, changes map[string]any) Response {
	p := map[string]any{"delegation_id": id, "api": "SYNO.Cal.Event", "method": "list", "version": 6, "params": map[string]any{"cal_id_list": []string{"/alice/home/"}}}
	if !long {
		p["job_id"] = "job1"
	}
	for k, v := range changes {
		p[k] = v
	}
	return offline(e, "webapi.call", p)
}
func TestWebAPIGrantsAndRunner(t *testing.T) {
	for _, long := range []bool{false, true} {
		t.Run(map[bool]string{false: "timed", true: "until-revoked"}[long], func(t *testing.T) {
			e, f := webSetup(t)
			id := webGrant(t, e, long)
			if r := webCall(e, id, long, nil); !r.OK {
				t.Fatalf("call: %+v", r)
			}
			if f.runner != "alice" || f.api != "SYNO.Cal.Event" || f.method != "list" || f.version != 6 || f.calls != 1 {
				t.Fatalf("wrong runner/call: %+v", f)
			}
			if f.apiCalls != 0 {
				t.Fatal("WebAPI grants must not require File Station")
			}
			if r := request(e, "delegate.revoke", map[string]any{"delegation_id": id}); !r.OK {
				t.Fatalf("revoke: %+v", r)
			}
			if r := webCall(e, id, long, nil); r.Code != "DELEGATION_DENIED" {
				t.Fatal(r)
			}
		})
	}
}
func TestWebAPIFailsClosed(t *testing.T) {
	cases := []struct {
		name, code string
		change     map[string]any
		mutate     func(*Executor, *webDSM)
	}{
		{"undeclared method", "POLICY_DENIED", map[string]any{"method": "set"}, nil},
		{"undeclared version", "POLICY_DENIED", map[string]any{"version": 5}, nil},
		{"core denied", "POLICY_DENIED", map[string]any{"api": "SYNO.Core.User"}, nil},
		{"runner injection", "INVALID_REQUEST", map[string]any{"params": map[string]any{"runner": "root"}}, nil},
		{"outfile injection", "INVALID_REQUEST", map[string]any{"params": map[string]any{"outfile": "/etc/passwd"}}, nil},
		{"api injection", "INVALID_REQUEST", map[string]any{"params": map[string]any{"api": "SYNO.Core.User"}}, nil},
		{"token injection", "INVALID_REQUEST", map[string]any{"params": map[string]any{"token": "secret"}}, nil},
		{"sid injection", "INVALID_REQUEST", map[string]any{"params": map[string]any{"_sid": "secret"}}, nil},
		{"mixed case auth injection", "INVALID_REQUEST", map[string]any{"params": map[string]any{"SynoToken": "secret"}}, nil},
		{"large params", "REQUEST_TOO_LARGE", map[string]any{"params": map[string]any{"value": strings.Repeat("a", 33<<10)}}, nil},
		{"wrong job", "DELEGATION_DENIED", map[string]any{"job_id": "other"}, nil},
		{"account disabled", "INELIGIBLE", nil, func(e *Executor, f *webDSM) { f.user.Expired = "now" }},
		{"uid reused", "INELIGIBLE", nil, func(e *Executor, f *webDSM) { f.user.UID++ }},
		{"app denied", "APP_DENIED", nil, func(e *Executor, f *webDSM) { f.privilegeErr = errors.New("denied") }},
		{"expired", "DELEGATION_DENIED", nil, func(e *Executor, f *webDSM) { e.Now = func() time.Time { return time.Unix(1060, 0) } }},
		{"changed profile", "DELEGATION_DENIED", nil, func(e *Executor, f *webDSM) {
			p := e.Policy.WebAPI["calendar"]
			p.Purpose = "Changed purpose"
			e.Policy.WebAPI["calendar"] = p
		}},
		{"disabled", "disabled", nil, func(e *Executor, f *webDSM) { e.Store.SetDisabled(true) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, f := webSetup(t)
			id := webGrant(t, e, false)
			if c.mutate != nil {
				c.mutate(e, f)
			}
			r := webCall(e, id, false, c.change)
			if r.Code != c.code || f.calls != 0 {
				t.Fatalf("%+v calls %d", r, f.calls)
			}
		})
	}
}
func TestWebAPIPolicyDenylist(t *testing.T) {
	for _, api := range []string{"SYNO.API.Auth", "SYNO.API.Request", "SYNO.Cal.Batch", "SYNO.Entry.Request", "SYNO.Core.User", "SYNO.FileStation.List", "SYNO.Office.Permission", "SYNO.Office.Node.Owner", "SYNO.SynologyDrive.TeamFolder", "SYNO.Package.Installation"} {
		e, _ := webSetup(t)
		e.Policy.WebAPI["calendar"] = WebAPIProfile{Purpose: "Forbidden", APIs: map[string]map[string][]int{api: {"get": {1}}}}
		if err := validatePolicy(e.Policy, e.Policy.Package); err == nil {
			t.Errorf("accepted %s", api)
		}
	}
	e, _ := webSetup(t)
	e.Policy.Operations = []string{"status", "delegate.create", "delegate.list", "delegate.revoke", "webapi.call"}
	if err := validatePolicy(e.Policy, e.Policy.Package); err != nil {
		t.Fatalf("WebAPI-only policy: %v", err)
	}
}
func TestWebAPIReturnsDSMEnvelopeAndNeverRetries(t *testing.T) {
	e, f := webSetup(t)
	id := webGrant(t, e, false)
	f.reply = json.RawMessage(`{"success":false,"error":{"code":9007}}`)
	r := webCall(e, id, false, nil)
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	if string(b) != string(f.reply) || f.calls != 1 {
		t.Fatalf("%s calls %d", b, f.calls)
	}
	f.err = context.DeadlineExceeded
	r = webCall(e, id, false, nil)
	if r.Code != "WEBAPI_UNCERTAIN" || f.calls != 2 {
		t.Fatal(r, f.calls)
	}
}

func TestWebAPIWireAndAdapter(t *testing.T) {
	for _, raw := range []string{
		`{"v":1,"op":"webapi.call","params":{"delegation_id":"x","api":"SYNO.Cal.Event","method":"list","version":6,"params":{"someOption":true}}}`,
	} {
		var request Request
		if err := strictJSON([]byte(raw), &request); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`{"v":1,"op":"webapi.call","params":{"params":{"a":1,"a":2}}}`,
		`{"v":1,"op":"webapi.call","auth":{"dsm_cookie":"a","DSM_COOKIE":"b"},"params":{}}`,
		`{"v":1,"op":"webapi.call","Params":{}}`,
	} {
		var request Request
		if strictJSON([]byte(raw), &request) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	c := &cmdStub{reply: `{"success":false,"error":{"code":9007}}`}
	d := &liveDSM{Command: c}
	out, err := d.WebAPI(context.Background(), "alice", "SYNO.Cal.Event", "list", 6, map[string]any{"cal_id_list": []string{"/alice/home/"}})
	if err != nil || !strings.Contains(string(out), "9007") || !contains(c.args, "runner=alice") || !contains(c.args, `cal_id_list=["/alice/home/"]`) {
		t.Fatalf("%s %v %v", out, err, c.args)
	}
	for _, reply := range []string{`{"success":true,"success":false}`, `[1,2]`, "HTTP/1.1 200 OK\n\nzip", strings.Repeat("x", maxWebAPIReply+1)} {
		c.reply = reply
		if _, err := d.WebAPI(context.Background(), "alice", "SYNO.Cal.Event", "list", 6, map[string]any{}); err == nil {
			t.Fatalf("accepted reply of %d bytes", len(reply))
		}
	}
}
