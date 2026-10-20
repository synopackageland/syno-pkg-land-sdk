package delegate

import (
	"context"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"sort"
)

func (s *Service) listOperations(ctx context.Context, caller api.Caller, auth map[string]string) (any, error) {
	owner, err := s.owner(caller)
	if err != nil {
		return nil, err
	}
	var grants struct {
		Delegations []Grant `json:"delegations"`
	}
	if err = s.Client.Call(ctx, "delegate.list", auth, nil, &grants); err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	for _, grant := range grants.Delegations {
		if grant.Subject.Username == owner.User && grant.Subject.UIDString() == owner.UID {
			owned[grant.DelegationID] = true
		}
	}
	if s.Store == nil {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	bindings, err := s.Store.MoveBindings(ctx)
	if err != nil {
		return nil, jobError(err)
	}
	out := []MoveMirror{}
	for _, binding := range bindings {
		if !owned[binding.DelegationID] {
			continue
		}
		var in MoveParams
		if strict(binding.Params, &in) != nil || !validMove(in) || in.JobID != binding.JobID || in.DelegationID != binding.DelegationID {
			return nil, fail("jobs_unavailable")
		}
		state := "uncertain"
		if binding.Phase == background.MoveReserved || binding.Phase == background.MoveAccepted {
			state = "queued"
		}
		if binding.CleanupDone {
			state = "committed"
		} else if binding.Refusal != "" {
			state = "interrupted"
		}
		if binding.Refusal != "" && !stableCode.MatchString(binding.Refusal) {
			return nil, fail("jobs_unavailable")
		}
		out = append(out, MoveMirror{JobID: binding.JobID, DelegationID: binding.DelegationID, Name: in.Name, State: state, Refusal: binding.Refusal})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JobID < out[j].JobID })
	return map[string]any{"operations": out}, nil
}
