package delegate

import (
	"context"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
	"time"
)

// DestinationSession verifies the authenticated caller and the write permission
// with the same request's DSM session. No session material is persisted.
type DestinationSession interface {
	CheckPermission(context.Context, api.Caller, *http.Request, string) error
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// SelectDestination makes only explicitly configured permanent destinations.
// Executor independently verifies the session, scope, policy and subject.
func (s *Service) selectDestination(ctx context.Context, caller api.Caller, r *http.Request, in SelectDestinationParams) (Grant, error) {
	var out Grant
	auth, err := api.DSMSession(r)
	if err != nil {
		return out, err
	}
	owner, err := s.owner(caller)
	if err != nil {
		return out, err
	}
	if s.Lifetime != "until-revoked" || !contains(s.Operations, "files.moveFromStaging") {
		return out, fail("unsupported")
	}
	if !contains(s.Destinations, in.Key) || !validPath(in.Path) || in.Path == "/" {
		return out, fail(CodeInvalidDestination)
	}
	// The executor's delegate.create revalidates this session, current subject,
	// App Privileges, approved scope and write permission before persisting a
	// grant. Do not replay the browser session through a File Station preflight.
	err = s.Client.Call(ctx, "delegate.create", auth, map[string]any{
		"ops": []string{"files.moveFromStaging"}, "scope": s.Scope, "path": in.Path,
		"destination": in.Key, "lifetime": "until-revoked",
	}, &out)
	if err != nil {
		return Grant{}, err
	}
	if out.DelegationID == "" || out.Subject.Username != owner.User || out.Subject.UIDString() != owner.UID || out.Lifetime != "until-revoked" || !out.ExpiresAt.IsZero() || out.Destination != in.Key || out.Path != in.Path {
		if out.DelegationID != "" {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			_ = s.Client.Call(cleanup, "delegate.revoke", auth, map[string]string{"delegation_id": out.DelegationID}, nil)
		}
		return Grant{}, fail(CodeExecutorProtocol)
	}
	return out, nil
}

func (s *Service) SelectDestination(ctx context.Context, caller api.Caller, r *http.Request, in SelectDestinationParams) (DestinationGrant, error) {
	grant, err := s.selectDestination(ctx, caller, r, in)
	if err != nil {
		return DestinationGrant{}, err
	}
	return DestinationGrant{ID: grant.DelegationID, Key: in.Key, Path: grant.Path, Username: grant.Subject.Username, Lifetime: grant.Lifetime}, nil
}

// verifyOperationOwner avoids writing even an App-owned operation mirror for
// another user's grant. The executor's session-filtered list supplies subjects;
// it still rechecks live grant/ACL/fence on every subsequent operation.
func (s *Service) verifyOperationOwner(ctx context.Context, caller api.Caller, auth map[string]string, id string) error {
	owner, err := s.owner(caller)
	if err != nil {
		return err
	}
	var out struct {
		Delegations []Grant `json:"delegations"`
	}
	if err = s.Client.Call(ctx, "delegate.list", auth, nil, &out); err != nil {
		return err
	}
	for _, grant := range out.Delegations {
		if grant.DelegationID == id && grant.Subject.Username == owner.User && grant.Subject.UIDString() == owner.UID {
			return nil
		}
	}
	return fail("DELEGATION_DENIED")
}
