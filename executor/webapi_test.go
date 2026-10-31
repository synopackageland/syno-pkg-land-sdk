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
		{"undeclared API", "POLICY_DENIED", map[string]any{"api": "SYNO.Core.User"}, nil},
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
func TestWebAPIOnlyPolicy(t *testing.T) {
	e, _ := webSetup(t)
	e.Policy.Operations = []string{"status", "delegate.create", "delegate.list", "delegate.revoke", "webapi.call"}
	if err := validatePolicy(e.Policy, e.Policy.Package); err != nil {
		t.Fatalf("WebAPI-only policy: %v", err)
	}
}

func TestWebAPIApprovedNames(t *testing.T) {
	for _, target := range []struct{ api, method string }{
		{"SYNO.API.Auth", "login"},
		{"SYNO.API.Request", "request"},
		{"SYNO.Cal.Batch", "get"},
		{"SYNO.Entry.Request", "get"},
		{"SYNO.Core.User", "get"},
		{"SYNO.FileStation.List", "list"},
		{"SYNO.Office.Permission", "get_permission"},
		{"SYNO.Office.Node.Owner", "set_owner"},
		{"SYNO.SynologyDrive.TeamFolder", "get_sharing"},
		{"SYNO.Package.Installation", "get"},
		{"SYNO.MailClient.Setting.SMTP", "list"},
		{"SYNO.MailPlusServer.Info", "get"},
		{"SYNO.Core.TaskScheduler", "list"},
		{"SYNO.Cal.Compound", "get"},
		{"SYNO.MailClient.Info", "get_compound_info"},
		{"SYNO.Cal.Event", "get_admin"},
	} {
		for _, mode := range []string{"json", "raw"} {
			t.Run(target.api+"/"+target.method+"/"+mode, func(t *testing.T) {
				e, f := webSetup(t)
				e.Policy.WebAPI["calendar"] = WebAPIProfile{Purpose: "Approved operation", RawMaxBytes: 1024, APIs: map[string]map[string][]int{target.api: {target.method: {1}}}}
				if err := validatePolicy(e.Policy, e.Policy.Package); err != nil {
					t.Fatal(err)
				}
				if mode == "raw" {
					e.DSM = &rawWebDSM{webDSM: f, stdout: []byte("Content-Type: application/octet-stream\r\n\r\nbody")}
					e.RootStaging = dirStaging{t.TempDir()}
				}
				id := webGrant(t, e, true)
				params := map[string]any{"api": target.api, "method": target.method, "version": 1, "response_type": mode}
				if r := webCall(e, id, true, params); !r.OK {
					t.Fatal(r)
				}
				if f.calls != 1 || f.runner != "alice" || f.api != target.api || f.method != target.method || f.version != 1 {
					t.Fatalf("wrong runner/call: %+v", f)
				}
				for key, value := range map[string]any{"api": "SYNO.Other.Info", "method": "undeclared", "version": 2} {
					original := params[key]
					params[key] = value
					if r := webCall(e, id, true, params); r.Code != "POLICY_DENIED" || f.calls != 1 {
						t.Fatalf("unpinned %s: %+v, calls %d", key, r, f.calls)
					}
					params[key] = original
				}
				c := &rawStreamCommand{cmdStub: cmdStub{reply: `{"success":true}`}, stdout: []byte("body")}
				d := &liveDSM{Command: c}
				if mode == "raw" {
					if err := d.WebAPIRaw(context.Background(), "alice", target.api, target.method, 1, map[string]any{}, &strings.Builder{}); err != nil {
						t.Fatal(err)
					}
				} else if _, err := d.WebAPI(context.Background(), "alice", target.api, target.method, 1, map[string]any{}); err != nil {
					t.Fatal(err)
				}
				if !contains(c.args, "runner=alice") || !contains(c.args, "api="+target.api) || !contains(c.args, "method="+target.method) || !contains(c.args, "version=1") {
					t.Fatal(c.args)
				}
			})
		}
	}
}

func TestWebAPINameFormats(t *testing.T) {
	for _, target := range []struct{ api, method string }{
		{"SYNO", "get"},
		{"SYNO..Info", "get"},
		{"SYNO.1Info", "get"},
		{"SYNO.Cal.Info;id", "get"},
		{"syno.Cal.Info", "get"},
		{"SYNO.Cal.Info", ""},
		{"SYNO.Cal.Info", "Get"},
		{"SYNO.Cal.Info", "get;id"},
		{"SYNO.Cal.Info", strings.Repeat("a", 65)},
	} {
		t.Run(target.api+"/"+target.method, func(t *testing.T) {
			e, _ := webSetup(t)
			e.Policy.WebAPI["calendar"] = WebAPIProfile{Purpose: "Invalid name", APIs: map[string]map[string][]int{target.api: {target.method: {1}}}}
			if err := validatePolicy(e.Policy, e.Policy.Package); err == nil {
				t.Fatal("accepted malformed profile")
			}
			if e.Policy.WebAPI["calendar"].allows(target.api, target.method, 1) {
				t.Fatal("accepted malformed target")
			}
			c := &rawStreamCommand{cmdStub: cmdStub{reply: `{"success":true}`}}
			d := &liveDSM{Command: c}
			if _, err := d.WebAPI(context.Background(), "alice", target.api, target.method, 1, map[string]any{}); err == nil {
				t.Fatal("JSON adapter accepted malformed target")
			}
			if err := d.WebAPIRaw(context.Background(), "alice", target.api, target.method, 1, map[string]any{}, &strings.Builder{}); err == nil {
				t.Fatal("raw adapter accepted malformed target")
			}
			if c.path != "" {
				t.Fatal("dispatched malformed target")
			}
		})
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
		// delegate.Client adds deadline_ms whenever its context has a deadline.
		`{"deadline_ms":2500,"v":1,"op":"webapi.call","params":{"delegation_id":"x","api":"SYNO.Cal.Event","method":"list","version":6,"params":{}}}`,
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
		`{"v":1,"op":"webapi.call","Deadline_ms":1,"params":{}}`,
		`{"v":1,"op":"webapi.call","deadline_ms":1,"deadline_ms":2,"params":{}}`,
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
