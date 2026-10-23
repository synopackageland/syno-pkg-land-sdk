package main

import "context"

// checkDestination is only a fresh permission observation of an existing
// moveFromStaging destination. It cannot create a grant, widen its path, read
// file content or select a runner. Unlike background moves it requires the
// current owner's browser session with the original client address.
func (e *Executor) checkDestination(ctx context.Context, r Request, p Params, d Delegation, hasJob bool) Response {
	if !contains(e.Policy.Operations, "files.moveFromStaging") {
		return deny("POLICY_DENIED")
	}
	if hasJob || p.Path != d.Path || !cleanPath(p.Path) {
		return deny("INVALID_REQUEST")
	}
	s, code := e.verified(ctx, r.Auth)
	if code != "" {
		return deny(code)
	}
	if s.Username != d.Subject.Username || s.UID != d.Subject.UID {
		return deny("DELEGATION_DENIED")
	}
	if _, code = e.validateMove(ctx, d); code != "" {
		return deny(code)
	}
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	if code = e.moveFenceLocked(d); code != "" {
		return deny(code)
	}
	return success(map[string]bool{"allowed": true})
}
