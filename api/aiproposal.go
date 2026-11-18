package api

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// AI write approval. A model call never runs a write: Tool.Gate returns a
// card, a proposal or a preview, and the App stores an AIProposal for it.
// The write runs only after a person confirms that proposal in the App's own
// UI, and the server checks the confirmation (principal, digests, expiry)
// with Confirm before it dispatches. Storage, UI and dispatch stay in the App.

// AIProposalVersion is the version of the AIProposal JSON shape.
const AIProposalVersion = 1

// Proposal lifetimes. DefaultProposalTTL applies when an App passes 0.
const (
	DefaultProposalTTL       = 10 * time.Minute
	MaxProposalTTL           = 15 * time.Minute
	DefaultProposalRetention = 30 * 24 * time.Hour
)

// ProposalState is where a proposal is in its life.
type ProposalState string

const (
	ProposalAwaiting   ProposalState = "awaiting-human" // waiting for a person
	ProposalUncertain  ProposalState = "uncertain"      // confirmed; save it durably, then dispatch. After a crash look the outcome up, never redispatch
	ProposalAccepted   ProposalState = "accepted"       // dispatched; a job is still running
	ProposalSucceeded  ProposalState = "succeeded"
	ProposalPartial    ProposalState = "partial" // some targets changed
	ProposalFailed     ProposalState = "failed"
	ProposalCanceled   ProposalState = "canceled"   // the person declined; never runs
	ProposalExpired    ProposalState = "expired"    // not confirmed before expires_at; never runs
	ProposalSuperseded ProposalState = "superseded" // replaced by another proposal; never runs
)

// Terminal reports whether nothing more happens to a proposal in this state.
func (s ProposalState) Terminal() bool {
	switch s {
	case ProposalSucceeded, ProposalPartial, ProposalFailed, ProposalCanceled, ProposalExpired, ProposalSuperseded:
		return true
	}
	return false
}

// Proposal errors. Confirm, Cancel and Finish wrap these; test with errors.Is.
var (
	ErrProposalClosed    = errors.New("proposal is no longer awaiting a person")
	ErrProposalExpired   = errors.New("proposal expired")
	ErrProposalPrincipal = errors.New("proposal belongs to another person")
	ErrProposalDigest    = errors.New("proposal changed since it was shown")
)

// AIProposal is the server-side record of one AI-requested write waiting for
// a person. It is private App state: show people the App's own card, and give
// the model only the ID and Receipt.ForModel.
type AIProposal struct {
	Version   int          `json:"version"`
	ID        string       `json:"id"` // 32 lowercase hex characters
	Principal string       `json:"principal"`
	Method    string       `json:"method"`
	Via       string       `json:"via"`
	Decision  GateDecision `json:"decision"` // card, propose or preview
	// ParamsDigest is DigestJSON of the bound params; PreviewDigest is the
	// App's DigestValue of what the person is shown (empty when none).
	ParamsDigest   string        `json:"params_digest"`
	PreviewDigest  string        `json:"preview_digest,omitempty"`
	AllowChanges   bool          `json:"allow_changes"`
	ConversationID string        `json:"conversation_id,omitempty"`
	TurnID         string        `json:"turn_id,omitempty"`
	ToolCallID     string        `json:"tool_call_id,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	ExpiresAt      time.Time     `json:"expires_at"`
	State          ProposalState `json:"state"`
	Receipt        *AIReceipt    `json:"receipt,omitempty"`
}

// AIReceipt is the outcome of a proposal. Summary is a short, non-secret
// sentence an App may report back to the model; Result is App-defined.
type AIReceipt struct {
	ProposalID  string          `json:"proposal_id"`
	State       ProposalState   `json:"state"`
	ConfirmedBy string          `json:"confirmed_by,omitempty"`
	ConfirmedAt time.Time       `json:"confirmed_at,omitzero"`
	FinishedAt  time.Time       `json:"finished_at,omitzero"`
	Code        string          `json:"code,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

// AIOutcome is what an App may tell the model about a proposal.
type AIOutcome struct {
	ProposalID string        `json:"proposal_id"`
	State      ProposalState `json:"state"`
	Summary    string        `json:"summary,omitempty"`
	Code       string        `json:"code,omitempty"`
}

// ForModel keeps the receipt's ID, state, summary and code; Result and the
// person who confirmed stay in the App.
func (r AIReceipt) ForModel() AIOutcome {
	return AIOutcome{ProposalID: r.ProposalID, State: r.State, Summary: r.Summary, Code: r.Code}
}

// AIConfirmation is what the server received from the person's click.
// PreviewDigest is the digest of the preview the page showed.
type AIConfirmation struct {
	Principal     string
	ParamsDigest  string
	PreviewDigest string
}

// Propose records an AI call that needs a person. It refuses unless Gate
// says card, propose or preview for this turn. params are the bound params
// (Bind); ttl 0 is DefaultProposalTTL and may not exceed MaxProposalTTL.
func (t Tool) Propose(turn Turn, principal string, params json.RawMessage, now time.Time, ttl time.Duration) (AIProposal, error) {
	decision := t.Gate(turn)
	switch decision {
	case GateCard, GatePropose, GatePreview:
	default:
		return AIProposal{}, fmt.Errorf("%s: gate %s needs no proposal", t.Method, decision)
	}
	if principal == "" {
		return AIProposal{}, fmt.Errorf("proposal needs the principal of the turn")
	}
	if ttl == 0 {
		ttl = DefaultProposalTTL
	}
	if ttl < 0 || ttl > MaxProposalTTL {
		return AIProposal{}, fmt.Errorf("proposal lifetime must be 0..%s", MaxProposalTTL)
	}
	if len(bytes.TrimSpace(params)) > 0 && bytes.TrimSpace(params)[0] != '{' {
		return AIProposal{}, fmt.Errorf("proposal params must be a JSON object")
	}
	digest, err := DigestJSON(params)
	if err != nil {
		return AIProposal{}, err
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return AIProposal{}, err
	}
	now = now.UTC()
	return AIProposal{
		Version: AIProposalVersion, ID: hex.EncodeToString(id), Principal: principal, Method: t.Method, Via: turn.Via,
		Decision: decision, ParamsDigest: digest, AllowChanges: turn.AllowChanges,
		ConversationID: turn.ConversationID, TurnID: turn.TurnID, ToolCallID: turn.ToolCallID,
		CreatedAt: now, ExpiresAt: now.Add(ttl), State: ProposalAwaiting,
	}, nil
}

// Expired reports whether the proposal can no longer be confirmed at now.
func (p AIProposal) Expired(now time.Time) bool { return !now.Before(p.ExpiresAt) }

// Expire moves an awaiting proposal past its expiry to expired. It reports
// whether it changed the proposal; save it when it did.
func (p *AIProposal) Expire(now time.Time) bool {
	if p.State != ProposalAwaiting || !p.Expired(now) {
		return false
	}
	p.close(ProposalExpired, now)
	return true
}

// Confirm checks a person's confirmation on the server, at the server's
// clock, and moves the proposal to uncertain. Save the proposal durably
// before dispatch, then call Finish with the outcome. A late confirmation
// expires the proposal (save it) and returns ErrProposalExpired.
func (p *AIProposal) Confirm(c AIConfirmation, now time.Time) error {
	if p.State != ProposalAwaiting {
		return fmt.Errorf("%w: %s", ErrProposalClosed, p.State)
	}
	if p.Expire(now) {
		return ErrProposalExpired
	}
	if c.Principal == "" || c.Principal != p.Principal {
		return ErrProposalPrincipal
	}
	if c.ParamsDigest != p.ParamsDigest || c.PreviewDigest != p.PreviewDigest {
		return ErrProposalDigest
	}
	now = now.UTC()
	p.State = ProposalUncertain
	p.Receipt = &AIReceipt{ProposalID: p.ID, State: ProposalUncertain, ConfirmedBy: c.Principal, ConfirmedAt: now}
	return nil
}

// Cancel records that the proposal's person declined it.
func (p *AIProposal) Cancel(principal string, now time.Time) error {
	if p.State != ProposalAwaiting {
		return fmt.Errorf("%w: %s", ErrProposalClosed, p.State)
	}
	if principal == "" || principal != p.Principal {
		return ErrProposalPrincipal
	}
	p.close(ProposalCanceled, now)
	return nil
}

// Supersede closes an awaiting proposal that a newer one replaced.
func (p *AIProposal) Supersede(now time.Time) error {
	if p.State != ProposalAwaiting {
		return fmt.Errorf("%w: %s", ErrProposalClosed, p.State)
	}
	p.close(ProposalSuperseded, now)
	return nil
}

func (p *AIProposal) close(state ProposalState, now time.Time) {
	p.State = state
	p.Receipt = &AIReceipt{ProposalID: p.ID, State: state, FinishedAt: now.UTC()}
}

// Finish records the dispatch outcome of a confirmed proposal: accepted
// while a job runs, then succeeded, partial or failed.
func (p *AIProposal) Finish(state ProposalState, summary, code string, result json.RawMessage, now time.Time) error {
	switch state {
	case ProposalAccepted, ProposalSucceeded, ProposalPartial, ProposalFailed:
	default:
		return fmt.Errorf("a dispatch cannot finish as %q", state)
	}
	if (p.State != ProposalUncertain && p.State != ProposalAccepted) || p.Receipt == nil || (state == ProposalAccepted && p.State == ProposalAccepted) {
		return fmt.Errorf("%w: %s", ErrProposalClosed, p.State)
	}
	p.State = state
	r := p.Receipt
	r.State, r.Summary, r.Code = state, summary, code
	if result != nil {
		r.Result = append(json.RawMessage(nil), result...)
	}
	if state.Terminal() {
		r.FinishedAt = now.UTC()
	}
	return nil
}

// Removable reports whether an App may delete the stored proposal: it ended
// at least retention ago (0 is DefaultProposalRetention), or it expired
// unconfirmed that long ago. Uncertain and accepted proposals are never
// removable: their outcome is still to be looked up.
func (p AIProposal) Removable(now time.Time, retention time.Duration) bool {
	if retention <= 0 {
		retention = DefaultProposalRetention
	}
	var ended time.Time
	switch {
	case p.State == ProposalAwaiting && p.Expired(now):
		ended = p.ExpiresAt
	case p.State.Terminal() && p.Receipt != nil:
		ended = p.Receipt.FinishedAt
	default:
		return false
	}
	return !now.Before(ended.Add(retention))
}

// DigestJSON is the lowercase hex SHA-256 of canonical JSON: object keys
// sorted, insignificant space removed, numbers kept as written. Empty input
// digests as {}.
func DigestJSON(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return "", fmt.Errorf("digest: invalid JSON: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return "", fmt.Errorf("digest: trailing data after JSON value")
	}
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:]), nil
}

// DigestValue is DigestJSON of v's JSON encoding, for a preview or snapshot.
func DigestValue(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return DigestJSON(raw)
}

// AI audit event kinds.
const (
	AIAuditToolCall = "ai.tool"     // one model tool call and its gate decision
	AIAuditProposal = "ai.proposal" // a proposal was created or changed state
)

// AIAuditEvent is the shared AI audit format. Apps add it to their own audit
// store (Fields for map-based records); the SDK stores nothing.
type AIAuditEvent struct {
	Event          string        `json:"event"`
	ProposedBy     string        `json:"proposed_by,omitempty"` // "ai" on the AI and MCP channels
	Via            string        `json:"via"`
	Method         string        `json:"method"`
	Decision       GateDecision  `json:"decision,omitempty"`
	AllowChanges   bool          `json:"allow_changes"`
	AuditClass     string        `json:"audit_class,omitempty"`
	ConversationID string        `json:"conversation_id,omitempty"`
	TurnID         string        `json:"turn_id,omitempty"`
	ToolCallID     string        `json:"tool_call_id,omitempty"`
	ProposalID     string        `json:"proposal_id,omitempty"`
	ParamsDigest   string        `json:"params_digest,omitempty"`
	State          ProposalState `json:"state,omitempty"`
	Actor          string        `json:"actor,omitempty"` // the person who confirmed
	Code           string        `json:"code,omitempty"`
	At             time.Time     `json:"at"`
}

func proposedBy(via string) string {
	if via == ViaAI || via == ViaMCP {
		return "ai"
	}
	return ""
}

// AuditCall describes one tool call for the App's audit: the gate decision,
// the turn's attribution and the digest of the bound params.
func (t Tool) AuditCall(turn Turn, params json.RawMessage, now time.Time) (AIAuditEvent, error) {
	digest, err := DigestJSON(params)
	if err != nil {
		return AIAuditEvent{}, err
	}
	return AIAuditEvent{
		Event: AIAuditToolCall, ProposedBy: proposedBy(turn.Via), Via: turn.Via, Method: t.Method,
		Decision: t.Gate(turn), AllowChanges: turn.AllowChanges, AuditClass: t.AuditClass(),
		ConversationID: turn.ConversationID, TurnID: turn.TurnID, ToolCallID: turn.ToolCallID,
		ParamsDigest: digest, At: now.UTC(),
	}, nil
}

// AuditEvent describes the proposal's current state for the App's audit.
// Record one when it is created and after every change of state, and add
// ProposalID to the App's business audit entry of the confirmed write.
func (p AIProposal) AuditEvent(now time.Time) AIAuditEvent {
	e := AIAuditEvent{
		Event: AIAuditProposal, ProposedBy: proposedBy(p.Via), Via: p.Via, Method: p.Method,
		Decision: p.Decision, AllowChanges: p.AllowChanges,
		ConversationID: p.ConversationID, TurnID: p.TurnID, ToolCallID: p.ToolCallID,
		ProposalID: p.ID, ParamsDigest: p.ParamsDigest, State: p.State, At: now.UTC(),
	}
	if p.Receipt != nil {
		e.Actor, e.Code = p.Receipt.ConfirmedBy, p.Receipt.Code
	}
	return e
}

// Fields returns the event as a map with its JSON names, leaving out empty
// optional fields. allow_changes is always present.
func (e AIAuditEvent) Fields() map[string]any {
	raw, _ := json.Marshal(e)
	fields := map[string]any{}
	_ = json.Unmarshal(raw, &fields)
	return fields
}
