package main

import (
	"context"
	"encoding/json"
)

const maxActiveRequests = 16
const maxOwnerRequests = 4

// Parsing/authentication has its own short, bounded connection pool. Long
// admitted operations no longer occupy every slot needed to identify a caller.
const maxPendingConnections = 64

type requestOwner struct {
	UID      int
	Username string
}

// admitRequest uses a verified DSM identity, or a protected offline grant's
// owner, never the cookie, browser address or a client-supplied username. The
// returned verification is scoped to this request, as for a root batch.
func (e *Executor) admitRequest(ctx context.Context, r Request) (context.Context, func(), string) {
	owner := requestOwner{UID: -1}
	if r.Auth.present() {
		s, code := e.verified(withRequestAddress(ctx, r.Auth), r.Auth)
		if code != "" {
			return ctx, nil, code
		}
		owner = requestOwner{s.UID, s.Username}
		ctx = context.WithValue(ctx, batchVerifiedKey{}, batchVerified{s, r.Auth})
	} else if e.Store != nil {
		var p struct {
			ID string `json:"delegation_id"`
		}
		if json.Unmarshal(r.Params, &p) == nil && idRE.MatchString(p.ID) {
			if d, err := e.Store.Get(p.ID); err == nil && d.Package == e.Policy.Package {
				owner = requestOwner{d.Subject.UID, d.Subject.Username}
			}
		}
	}
	e.admissionMu.Lock()
	defer e.admissionMu.Unlock()
	if e.activeRequests >= maxActiveRequests || e.ownerRequests[owner] >= maxOwnerRequests {
		return ctx, nil, "EXECUTOR_PAUSED"
	}
	if e.ownerRequests == nil {
		e.ownerRequests = map[requestOwner]int{}
	}
	e.activeRequests++
	e.ownerRequests[owner]++
	return ctx, func() {
		e.admissionMu.Lock()
		defer e.admissionMu.Unlock()
		e.activeRequests--
		if e.ownerRequests[owner] <= 1 {
			delete(e.ownerRequests, owner)
		} else {
			e.ownerRequests[owner]--
		}
	}, ""
}
