package delegate

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
	"os"
	"strings"
)

func virtualScope(scope string) string {
	if scope == "home" {
		return "/home"
	}
	if strings.HasPrefix(scope, "shared:") {
		name := strings.TrimPrefix(scope, "shared:")
		if validName(name) && !strings.ContainsAny(name, "\\:") && strings.TrimSpace(name) == name {
			return "/" + name
		}
	}
	return ""
}
func validSelection(value *ScopeSelection, dynamic bool) bool {
	return value != nil && virtualScope(value.Scope) != "" && value.Root == virtualScope(value.Scope) && value.Generation <= 1<<53-1 && (!dynamic || value.Generation > 0)
}

// Never use this package-owned draft for authorization or executor parameters.
func (s *Service) decorateScope(out *Capability) {
	out.ScopeMode = s.Scope
	if s.Scope != "install-selected" || s.InstallDraftPath == "" {
		return
	}
	info, err := os.Lstat(s.InstallDraftPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return
	}
	b, err := os.ReadFile(s.InstallDraftPath)
	var draft struct {
		Schema int    `json:"schema"`
		Scope  string `json:"scope"`
	}
	if err != nil || len(b) > 4096 || json.Unmarshal(b, &draft) != nil || draft.Schema != 1 || virtualScope(draft.Scope) == "" {
		return
	}
	out.Preselection = draft.Scope
	out.Mismatch = out.Selection != nil && out.Selection.Scope != draft.Scope
}

// Destination is the single caller-bound lookup for the chosen scope,
// destination and grant status. Selection comes from matching executor pins.
// A destination whose only grants were revoked is revoked; one whose grant no
// longer fits the selected scope needs reselection.
func (s *Service) Destination(ctx context.Context, caller api.Caller, r *http.Request, key string) (DestinationState, error) {
	out := DestinationState{Key: key, Status: "missing"}
	if s.Lifetime != "until-revoked" || !contains(s.Destinations, key) {
		return out, fail(CodeInvalidDestination)
	}
	owner, err := s.owner(caller)
	if err != nil {
		return out, err
	}
	auth, err := api.DSMSession(r)
	if err != nil {
		return out, err
	}
	status := s.Status(ctx, auth)
	if status.State == "needs-scope-confirmation" {
		out.Status = status.State
		return out, nil
	}
	if status.Reason == CodeAppDenied || lifecycle(status.Reason) {
		return out, fail(status.Reason)
	}
	if status.State != "ready" && status.State != "disabled" {
		return out, fail(CodeExecutorUnavailable)
	}
	out.Selection = status.Selection
	if !validSelection(out.Selection, s.Scope == "install-selected") {
		return out, fail(CodeExecutorProtocol)
	}
	var list struct {
		Delegations []Grant `json:"delegations"`
	}
	if err = s.Client.Call(ctx, "delegate.list", auth, nil, &list); err != nil {
		return out, err
	}
	for _, grant := range list.Delegations {
		if grant.Subject.Username != owner.User || grant.Subject.UIDString() != owner.UID || grant.Destination != key || grant.Lifetime != "until-revoked" {
			continue
		}
		if grant.Revoked {
			if out.Status == "missing" {
				out.Status = "revoked"
			}
			continue
		}
		if out.Destination == nil {
			out.Status = "reselection-required"
		}
		if grant.Scope != out.Selection.Scope || grant.ScopeGeneration != out.Selection.Generation || !validPath(grant.Path) || !(grant.Path == out.Selection.Root || strings.HasPrefix(grant.Path, out.Selection.Root+"/")) {
			continue
		}
		out.Destination = &DestinationGrant{ID: grant.DelegationID, Key: key, Path: grant.Path, Username: grant.Subject.Username, Lifetime: grant.Lifetime}
		out.Status = "active"
	}
	if status.State == "disabled" {
		out.Status = "disabled"
		out.Destination = nil
	}
	return out, nil
}
