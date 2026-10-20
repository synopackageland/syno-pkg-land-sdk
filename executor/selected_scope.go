package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

func validSelectedScope(scope string) bool {
	if scope == "home" {
		return true
	}
	if !strings.HasPrefix(scope, "shared:") {
		return false
	}
	name := strings.TrimPrefix(scope, "shared:")
	return validName(name) && name != "." && strings.TrimSpace(name) == name && !strings.ContainsAny(name, "\\:%") && !strings.EqualFold(name, "home") && !strings.EqualFold(name, "homes")
}
func (s *Store) SelectedScope() (SelectedScope, error) {
	var out SelectedScope
	if err := s.record("selected-scope.record", &out); err != nil {
		return out, err
	}
	if out.Schema != 1 || !validSelectedScope(out.Scope) || out.Generation == 0 || out.Generation > maxScopeGeneration {
		return SelectedScope{}, errors.New("selected scope state")
	}
	return out, nil
}

// Caller holds commitMu; root selection and publish share the same fence.
func (e *Executor) selectionLocked() (SelectedScope, string) {
	if e.Policy.Scope != installSelectedScope {
		return SelectedScope{Schema: 1, Scope: e.Policy.Scope}, ""
	}
	state, err := e.Store.SelectedScope()
	if os.IsNotExist(err) {
		return SelectedScope{}, "needs-scope-confirmation"
	}
	if err != nil {
		return SelectedScope{}, "STORAGE_ERROR"
	}
	return state, ""
}
func (e *Executor) selection() (SelectedScope, string) {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	return e.selectionLocked()
}
func (e *Executor) scopeMatchesLocked(d Delegation) bool {
	if isWebGrant(d) {
		return e.webGrantMatches(d)
	}
	state, code := e.selectionLocked()
	return code == "" && state.Scope == d.Scope && state.Generation == d.ScopeGeneration
}
func (e *Executor) scopeMatches(d Delegation) bool {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	return e.scopeMatchesLocked(d)
}

// Only a kernel-verified root peer can reach this operation. It is absent from
// the public wire allowlist, even when a package supplies an administrator SID.
func (e *Executor) handlePeer(ctx context.Context, r Request, peerUID, packageUID int) Response {
	if peerUID == 0 && r.Op == "delegate.setScope" {
		if r.Version != 1 || r.Auth.present() {
			return deny("INVALID_REQUEST")
		}
		return e.setSelectedScope(withClientAddress(ctx, localAddress), r.Params)
	}
	if peerUID != packageUID {
		return deny("PEER_DENIED")
	}
	return e.Handle(ctx, r)
}
func (e *Executor) setSelectedScope(ctx context.Context, raw json.RawMessage) Response {
	if ctx.Err() != nil {
		return deny("EXECUTOR_PAUSED")
	}
	if e.Policy.Scope != installSelectedScope || e.Policy.Lifetime != "until-revoked" {
		return deny("POLICY_DENIED")
	}
	var in struct {
		Scope              string  `json:"scope"`
		Actor              string  `json:"actor"`
		ExpectedGeneration *uint64 `json:"expected_generation"`
	}
	if strictJSON(raw, &in) != nil || !validSelectedScope(in.Scope) || in.Actor == "" || in.ExpectedGeneration == nil || *in.ExpectedGeneration > maxScopeGeneration {
		return deny("INVALID_REQUEST")
	}
	actor, err := e.DSM.Account(ctx, in.Actor)
	if errors.Is(err, verificationUnavailable) {
		return deny("INTERNAL")
	}
	if err != nil || actor.Username != in.Actor || !actor.Admin || actor.Expired != "normal" || actor.UID < 1024 {
		return deny("ADMIN_REQUIRED")
	}
	if err := e.DSM.Allow(ctx, actor, e.Policy.App, e.Policy.Entry); err != nil {
		return deny(dsmRefusal(err, "APP_DENIED"))
	}
	// Scope selection grants no data permission. Resolve existence only; each
	// destination and subsequent write still checks its owner's current ACL/UID.
	if in.Scope != "home" {
		if _, err = e.DSM.ScopeRoot(ctx, actor, in.Scope); err != nil {
			return deny("SCOPE_DENIED")
		}
	}
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	old, err := e.Store.SelectedScope()
	if err != nil && !os.IsNotExist(err) {
		return deny("STORAGE_ERROR")
	}
	if old.Generation != *in.ExpectedGeneration {
		return deny("SCOPE_APPROVAL_STALE")
	}
	if err == nil && old.Scope == in.Scope {
		return success(old)
	}
	if old.Generation >= maxScopeGeneration {
		return deny("STORAGE_ERROR")
	}
	next := SelectedScope{Schema: 1, Scope: in.Scope, Generation: old.Generation + 1}
	if e.auditLocked(actor, "delegate.scope.confirm", "", scopeVirtual(in.Scope), "admitting") != nil {
		return deny("STORAGE_ERROR")
	}
	b, _ := json.Marshal(next)
	if e.Store.atomic("selected-scope.record", b) != nil {
		return deny("STORAGE_ERROR")
	}
	// New generation is already authoritative if cleanup fails. Old grants fail
	// admission/publish before acknowledgement, and switching back cannot revive them.
	grants, err := e.Store.List()
	if err != nil {
		return deny("STORAGE_ERROR")
	}
	for _, d := range grants {
		if d.Package == e.Policy.Package && !d.Revoked {
			d.Revoked = true
			if e.Store.Put(d) != nil {
				return deny("STORAGE_ERROR")
			}
			e.cancelGrantLocked(d.ID, "scope-changed")
		}
	}
	if e.auditLocked(actor, "delegate.scope.confirm", "", scopeVirtual(in.Scope), "confirmed") != nil {
		return deny("STORAGE_ERROR")
	}
	return success(next)
}
