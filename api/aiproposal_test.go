package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func testTools(t *testing.T) map[string]Tool {
	t.Helper()
	tools, err := testCatalog(t).Tools("app")
	if err != nil {
		t.Fatal(err)
	}
	byMethod := map[string]Tool{}
	for _, tool := range tools {
		byMethod[tool.Method] = tool
	}
	return byMethod
}

// No AI write runs without a person's click: on the AI and MCP channels a
// write operation never gets GateRun, whatever Allow changes says.
func TestNoAIWriteRunsWithoutHumanClick(t *testing.T) {
	for method, tool := range testTools(t) {
		if tool.Operation.Effect == "read" {
			continue
		}
		for _, turn := range []Turn{{Via: ViaAI}, {Via: ViaAI, AllowChanges: true}, {Via: ViaMCP}, {Via: ViaMCP, AllowChanges: true}} {
			if got := tool.Gate(turn); got == GateRun {
				t.Errorf("%s %+v runs an AI write", method, turn)
			}
		}
	}
}

func TestDigestJSONIsCanonical(t *testing.T) {
	a, err := DigestJSON(json.RawMessage(`{"b":1,"a":{"y":[1,2],"x":1.5}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := DigestJSON(json.RawMessage("{ \"a\": {\"x\":1.5, \"y\":[1,2]},\n \"b\":1 }"))
	if a != b || len(a) != 64 || strings.ToLower(a) != a {
		t.Fatalf("digest %q %q", a, b)
	}
	c, _ := DigestJSON(json.RawMessage(`{"b":1,"a":{"y":[2,1],"x":1.5}}`))
	if c == a {
		t.Fatal("array order ignored")
	}
	empty, _ := DigestJSON(nil)
	obj, _ := DigestJSON(json.RawMessage(`{}`))
	if empty != obj {
		t.Fatal("empty params differ from {}")
	}
	if _, err = DigestJSON(json.RawMessage(`{"a":`)); err == nil {
		t.Fatal("invalid JSON digested")
	}
	if _, err = DigestJSON(json.RawMessage(`{"a":1} {"b":2}`)); err == nil {
		t.Fatal("trailing value digested")
	}
	v, err := DigestValue(map[string]any{"b": 1, "a": map[string]any{"x": 1.5, "y": []int{1, 2}}})
	if err != nil || v != a {
		t.Fatalf("value digest %q %v", v, err)
	}
}

func newTestProposal(t *testing.T, method string, turn Turn, now time.Time) AIProposal {
	t.Helper()
	p, err := testTools(t)[method].Propose(turn, "alice", json.RawMessage(`{"id":"n1","text":"hi"}`), now, 0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProposeOnlyForHumanDecisions(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	turn := Turn{Via: ViaAI, AllowChanges: true, ConversationID: "c1", TurnID: "t1", ToolCallID: "call-1"}
	p := newTestProposal(t, "notes.save", turn, now)
	if p.Version != AIProposalVersion || len(p.ID) != 32 || p.Method != "notes.save" || p.Principal != "alice" || p.Decision != GateCard || p.State != ProposalAwaiting {
		t.Fatalf("proposal %+v", p)
	}
	if !p.AllowChanges || p.Via != ViaAI || p.ConversationID != "c1" || p.TurnID != "t1" || p.ToolCallID != "call-1" {
		t.Fatalf("attribution %+v", p)
	}
	if !p.CreatedAt.Equal(now) || !p.ExpiresAt.Equal(now.Add(DefaultProposalTTL)) {
		t.Fatalf("times %v %v", p.CreatedAt, p.ExpiresAt)
	}
	want, _ := DigestJSON(json.RawMessage(`{"text":"hi","id":"n1"}`))
	if p.ParamsDigest != want {
		t.Fatal("params digest")
	}
	if q := newTestProposal(t, "notes.save", turn, now); q.ID == p.ID {
		t.Fatal("proposal ids repeat")
	}
	if p := newTestProposal(t, "notes.propose", turn, now); p.Decision != GatePropose {
		t.Fatalf("propose decision %s", p.Decision)
	}
	tools := testTools(t)
	for _, c := range []struct {
		method string
		turn   Turn
	}{
		{"notes.list", turn},                                  // read runs; no proposal
		{"notes.propose", Turn{Via: ViaAI}},                   // intent required
		{"notes.save", Turn{Via: ViaMCP}},                     // refused
		{"notes.save", Turn{Via: ViaCtl, AllowChanges: true}}, // a person runs it
	} {
		if _, err := tools[c.method].Propose(c.turn, "alice", nil, now, 0); err == nil {
			t.Errorf("%s %+v made a proposal", c.method, c.turn)
		}
	}
	if _, err := tools["notes.save"].Propose(turn, "", nil, now, 0); err == nil {
		t.Fatal("proposal without principal")
	}
	if _, err := tools["notes.save"].Propose(turn, "alice", json.RawMessage(`[]`), now, 0); err == nil {
		t.Fatal("non-object params")
	}
	for _, ttl := range []time.Duration{-time.Second, MaxProposalTTL + time.Second} {
		if _, err := tools["notes.save"].Propose(turn, "alice", nil, now, ttl); err == nil {
			t.Fatalf("ttl %v accepted", ttl)
		}
	}
	if p, _ := tools["notes.save"].Propose(turn, "alice", nil, now, time.Minute); !p.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatal("custom ttl")
	}
}

func TestProposalConfirmLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	p := newTestProposal(t, "notes.save", Turn{Via: ViaAI, ConversationID: "c1", TurnID: "t1", ToolCallID: "k"}, now)
	p.PreviewDigest, _ = DigestValue(map[string]any{"before": "a", "after": "b"})
	ok := AIConfirmation{Principal: "alice", ParamsDigest: p.ParamsDigest, PreviewDigest: p.PreviewDigest}

	bad := []struct {
		name string
		c    AIConfirmation
		want error
	}{
		{"other person", AIConfirmation{Principal: "bob", ParamsDigest: ok.ParamsDigest, PreviewDigest: ok.PreviewDigest}, ErrProposalPrincipal},
		{"changed params", AIConfirmation{Principal: "alice", ParamsDigest: strings.Repeat("0", 64), PreviewDigest: ok.PreviewDigest}, ErrProposalDigest},
		{"stale preview", AIConfirmation{Principal: "alice", ParamsDigest: ok.ParamsDigest, PreviewDigest: strings.Repeat("0", 64)}, ErrProposalDigest},
		{"no preview", AIConfirmation{Principal: "alice", ParamsDigest: ok.ParamsDigest}, ErrProposalDigest},
	}
	for _, b := range bad {
		q := p
		if err := q.Confirm(b.c, now.Add(time.Minute)); !errors.Is(err, b.want) || q.State != ProposalAwaiting || q.Receipt != nil {
			t.Fatalf("%s: %v %+v", b.name, err, q)
		}
	}
	if err := p.Confirm(ok, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if p.State != ProposalUncertain || p.Receipt == nil || p.Receipt.ProposalID != p.ID || p.Receipt.ConfirmedBy != "alice" || !p.Receipt.ConfirmedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("confirmed %+v %+v", p, p.Receipt)
	}
	if err := p.Confirm(ok, now.Add(time.Minute)); !errors.Is(err, ErrProposalClosed) {
		t.Fatalf("second confirm: %v", err)
	}
	if err := p.Finish(ProposalAccepted, "Job started", "", json.RawMessage(`{"job":"j1"}`), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := p.Finish(ProposalSucceeded, "Saved note n1", "", json.RawMessage(`{"undo_id":"e1"}`), now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if p.State != ProposalSucceeded || p.Receipt.State != ProposalSucceeded || p.Receipt.Summary != "Saved note n1" || !p.Receipt.FinishedAt.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("finished %+v", p.Receipt)
	}
	if err := p.Finish(ProposalFailed, "", "x", nil, now); !errors.Is(err, ErrProposalClosed) {
		t.Fatal("finished twice")
	}
	if err := p.Cancel("alice", now); !errors.Is(err, ErrProposalClosed) {
		t.Fatal("canceled after finish")
	}
	out := p.Receipt.ForModel()
	raw, _ := json.Marshal(out)
	if string(raw) != `{"proposal_id":"`+p.ID+`","state":"succeeded","summary":"Saved note n1"}` {
		t.Fatalf("model report %s", raw)
	}
}

func TestProposalExpiryIsServerSide(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	p := newTestProposal(t, "notes.save", Turn{Via: ViaAI}, now)
	c := AIConfirmation{Principal: "alice", ParamsDigest: p.ParamsDigest}
	if p.Expired(p.ExpiresAt.Add(-time.Nanosecond)) || !p.Expired(p.ExpiresAt) {
		t.Fatal("expiry boundary")
	}
	if err := p.Confirm(c, p.ExpiresAt); !errors.Is(err, ErrProposalExpired) {
		t.Fatalf("late confirm: %v", err)
	}
	if p.State != ProposalExpired || p.Receipt == nil || p.Receipt.State != ProposalExpired || p.Receipt.ConfirmedBy != "" {
		t.Fatalf("expired %+v %+v", p, p.Receipt)
	}
	if err := p.Confirm(c, now); !errors.Is(err, ErrProposalClosed) {
		t.Fatal("expired proposal confirmed with an earlier clock")
	}
	q := newTestProposal(t, "notes.save", Turn{Via: ViaAI}, now)
	if q.Expire(now) || q.State != ProposalAwaiting {
		t.Fatal("expired early")
	}
	if !q.Expire(q.ExpiresAt) || q.State != ProposalExpired || q.Expire(q.ExpiresAt) {
		t.Fatal("expire sweep")
	}
}

func TestProposalCancelSupersede(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	p := newTestProposal(t, "notes.save", Turn{Via: ViaAI}, now)
	if err := p.Cancel("bob", now); !errors.Is(err, ErrProposalPrincipal) {
		t.Fatal("other person canceled")
	}
	if err := p.Cancel("alice", now); err != nil || p.State != ProposalCanceled || p.Receipt.State != ProposalCanceled {
		t.Fatalf("cancel %v %+v", err, p)
	}
	if err := p.Confirm(AIConfirmation{Principal: "alice", ParamsDigest: p.ParamsDigest}, now); !errors.Is(err, ErrProposalClosed) {
		t.Fatal("canceled proposal confirmed")
	}
	q := newTestProposal(t, "notes.save", Turn{Via: ViaAI}, now)
	if err := q.Supersede(now); err != nil || q.State != ProposalSuperseded {
		t.Fatalf("supersede %v", err)
	}
	r := newTestProposal(t, "notes.save", Turn{Via: ViaAI}, now)
	_ = r.Confirm(AIConfirmation{Principal: "alice", ParamsDigest: r.ParamsDigest}, now)
	if err := r.Cancel("alice", now); !errors.Is(err, ErrProposalClosed) {
		t.Fatal("canceled while running")
	}
	for _, bad := range []ProposalState{ProposalAwaiting, ProposalUncertain, ProposalCanceled, ProposalExpired, "done"} {
		if err := r.Finish(bad, "", "", nil, now); err == nil {
			t.Fatalf("finished as %s", bad)
		}
	}
}

func TestProposalRetention(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	keep := 24 * time.Hour
	open := newTestProposal(t, "notes.save", Turn{Via: ViaAI}, now)
	if open.Removable(open.ExpiresAt.Add(-time.Second), keep) {
		t.Fatal("open proposal removed")
	}
	done := newTestProposal(t, "notes.save", Turn{Via: ViaAI}, now)
	_ = done.Confirm(AIConfirmation{Principal: "alice", ParamsDigest: done.ParamsDigest}, now)
	if done.Removable(now.Add(365*24*time.Hour), keep) {
		t.Fatal("uncertain proposal removed")
	}
	_ = done.Finish(ProposalSucceeded, "ok", "", nil, now.Add(time.Minute))
	if done.Removable(now.Add(time.Minute+keep-time.Second), keep) || !done.Removable(now.Add(time.Minute+keep), keep) {
		t.Fatal("finished retention")
	}
	expired := newTestProposal(t, "notes.save", Turn{Via: ViaAI}, now)
	if !expired.Removable(expired.ExpiresAt.Add(keep), keep) {
		t.Fatal("an unswept expired proposal stays forever")
	}
	if done.Removable(now.Add(10*365*24*time.Hour), 0) != done.Removable(now.Add(10*365*24*time.Hour), DefaultProposalRetention) {
		t.Fatal("zero retention uses the default")
	}
}

func TestAIAuditEvents(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	tools := testTools(t)
	turn := Turn{Via: ViaAI, AllowChanges: true, ConversationID: "c1", TurnID: "t1", ToolCallID: "call-1"}
	params := json.RawMessage(`{"id":"n1"}`)
	call, err := tools["notes.list"].AuditCall(turn, params, now)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := DigestJSON(params)
	if call.Event != AIAuditToolCall || call.ProposedBy != "ai" || call.Via != ViaAI || call.Method != "notes.list" || call.Decision != GateRun || !call.AllowChanges || call.ConversationID != "c1" || call.TurnID != "t1" || call.ToolCallID != "call-1" || call.ParamsDigest != digest || call.ProposalID != "" || call.AuditClass != "read" {
		t.Fatalf("call event %+v", call)
	}
	fields := call.Fields()
	if fields["proposed_by"] != "ai" || fields["allow_changes"] != true || fields["params_digest"] != digest || fields["decision"] != "run" {
		t.Fatalf("fields %v", fields)
	}
	if _, ok := fields["proposal_id"]; ok {
		t.Fatal("empty fields kept")
	}
	off, _ := tools["notes.list"].AuditCall(Turn{Via: ViaAI}, params, now)
	if off.Fields()["allow_changes"] != false {
		t.Fatal("allow_changes off must be recorded")
	}
	ctl, _ := tools["notes.save"].AuditCall(Turn{Via: ViaCtl, AllowChanges: true}, params, now)
	if ctl.ProposedBy != "" || ctl.Decision != GateRun {
		t.Fatalf("ctl event %+v", ctl)
	}
	mcp, _ := tools["notes.list"].AuditCall(Turn{Via: ViaMCP}, params, now)
	if mcp.ProposedBy != "ai" {
		t.Fatal("mcp is an AI client")
	}

	p := newTestProposal(t, "notes.save", turn, now)
	created := p.AuditEvent(now)
	if created.Event != AIAuditProposal || created.ProposalID != p.ID || created.State != ProposalAwaiting || created.ProposedBy != "ai" || created.Decision != GateCard || created.ParamsDigest != p.ParamsDigest || created.ToolCallID != "call-1" || created.Actor != "" || created.AuditClass != "" {
		t.Fatalf("created %+v", created)
	}
	_ = p.Confirm(AIConfirmation{Principal: "alice", ParamsDigest: p.ParamsDigest}, now)
	_ = p.Finish(ProposalFailed, "Note is locked", "locked", nil, now)
	finished := p.AuditEvent(now)
	if finished.State != ProposalFailed || finished.Actor != "alice" || finished.Code != "locked" {
		t.Fatalf("finished %+v", finished)
	}
	raw, _ := json.Marshal(finished)
	var back AIAuditEvent
	if json.Unmarshal(raw, &back) != nil || back.ProposalID != p.ID || back.ProposedBy != "ai" || !back.At.Equal(now) {
		t.Fatalf("round trip %s", raw)
	}
	if _, err := tools["notes.list"].AuditCall(turn, json.RawMessage(`{`), now); err == nil {
		t.Fatal("bad params audited")
	}
}

func TestProposalJSONShape(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	p := newTestProposal(t, "notes.save", Turn{Via: ViaAI, ConversationID: "c1", TurnID: "t1", ToolCallID: "k"}, now)
	raw, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	for _, key := range []string{"version", "id", "principal", "method", "via", "decision", "params_digest", "allow_changes", "conversation_id", "turn_id", "tool_call_id", "created_at", "expires_at", "state"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing %s in %s", key, raw)
		}
	}
	var back AIProposal
	if err := json.Unmarshal(raw, &back); err != nil || back.ID != p.ID || !back.ExpiresAt.Equal(p.ExpiresAt) {
		t.Fatalf("round trip %v", err)
	}
}
