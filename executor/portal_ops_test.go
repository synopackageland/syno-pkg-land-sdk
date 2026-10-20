package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakePortal struct {
	entry                  string
	err                    error
	registers, unregisters int
}

func (p *fakePortal) Register(context.Context) error {
	p.registers++
	if p.err == nil {
		p.entry = PortalRegistered
	}
	return p.err
}
func (p *fakePortal) Unregister(context.Context) error {
	p.unregisters++
	if p.err == nil {
		p.entry = PortalUnregistered
	}
	return p.err
}
func (p *fakePortal) Entry() string { return p.entry }

func portalSetup(t *testing.T) (*Executor, *fakeDSM, *fakePortal) {
	t.Helper()
	e, f, _ := setup(t)
	e.Policy = portalPolicy("p2probe")
	f.user.Admin = true
	p := &fakePortal{entry: PortalUnregistered}
	e.Portal = p
	return e, f, p
}

func TestPortalOpsChangeTheEntryForAnAdministrator(t *testing.T) {
	e, _, p := portalSetup(t)
	out := request(e, "portal.register", nil)
	if !out.OK || p.registers != 1 {
		t.Fatal(out)
	}
	if b, _ := json.Marshal(out.Result); string(b) != `{"entry":"registered"}` {
		t.Fatal(string(b))
	}
	out = request(e, "portal.unregister", map[string]any{})
	if !out.OK || p.unregisters != 1 {
		t.Fatal(out)
	}
	if b, _ := json.Marshal(out.Result); string(b) != `{"entry":"unregistered"}` {
		t.Fatal(string(b))
	}
}

func TestPortalOpsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Executor, *fakeDSM, *fakePortal)
		params any
		code   string
	}{
		{"not an administrator", func(e *Executor, f *fakeDSM, p *fakePortal) { f.user.Admin = false }, nil, "ADMIN_REQUIRED"},
		{"app denied", func(e *Executor, f *fakeDSM, p *fakePortal) { f.privilegeErr = errors.New("deny") }, nil, "APP_DENIED"},
		{"expired account", func(e *Executor, f *fakeDSM, p *fakePortal) { f.user.Expired = "now" }, nil, "AUTH_DENIED"},
		{"not in the policy", func(e *Executor, f *fakeDSM, p *fakePortal) {
			e.Policy.Operations = []string{"status"}
		}, nil, "POLICY_DENIED"},
		{"a parameter", nil, map[string]any{"name": "x"}, "INVALID_REQUEST"},
		{"DSM refuses", func(e *Executor, f *fakeDSM, p *fakePortal) { p.err = errors.New("nginx -t") }, nil, "PORTAL_FAILED"},
		{"no portal", func(e *Executor, f *fakeDSM, p *fakePortal) { e.Portal = nil }, nil, "PORTAL_FAILED"},
	} {
		for _, op := range []string{"portal.register", "portal.unregister"} {
			e, f, p := portalSetup(t)
			if tc.mutate != nil {
				tc.mutate(e, f, p)
			}
			if out := request(e, op, tc.params); out.Code != tc.code {
				t.Fatal(tc.name, op, out)
			}
			if tc.code != "PORTAL_FAILED" && p.registers+p.unregisters != 0 {
				t.Fatal(tc.name, op, "changed the entry")
			}
		}
	}
}

func TestPortalOpsNeedASession(t *testing.T) {
	e, _, p := portalSetup(t)
	out := e.Handle(context.Background(), Request{Version: 1, Op: "portal.register", Params: json.RawMessage(`{}`)})
	if out.Code != "AUTH_DENIED" || p.registers != 0 {
		t.Fatal(out)
	}
}

func TestStatusReportsThePortalEntry(t *testing.T) {
	e, _, p := portalSetup(t)
	p.entry = PortalCleanupRequired
	out := request(e, "status", nil)
	if r, _ := out.Result.(map[string]any); !out.OK || r["portal"] != PortalCleanupRequired {
		t.Fatal(out)
	}
	e.Portal = nil
	out = request(e, "status", nil)
	if r, _ := out.Result.(map[string]any); r["portal"] != PortalCleanupRequired {
		t.Fatal("a policy with a portal reports it without the entry", out)
	}
	plain, _, _ := setup(t)
	out = request(plain, "status", nil)
	if r, _ := out.Result.(map[string]any); r["portal"] != nil {
		t.Fatal("portal without a portal policy", out)
	}
}
