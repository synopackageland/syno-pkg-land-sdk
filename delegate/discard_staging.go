package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
)

// DiscardStaging removes the staging source of a move that will never copy it:
// refused at admission, canceled or interrupted. Such a source is kept so the
// App can save it again under a new job; call this when the App deletes the
// job. A committed move's source is already removed. A source that another
// pending job of the App pins is kept (STAGING_IN_USE), as is the source of a
// move that may still copy (operation-active) or whose result is unknown
// (uncertain). Only the pinned staging file is removed, and the folders it
// leaves empty; the move's mirror is then finished history.
func (s *Service) DiscardStaging(ctx context.Context, ref OperationRef) error {
	if !validRef(ref) {
		return fail(CodeInvalidRequest)
	}
	if s.Store == nil {
		return fail("STAGING_UNAVAILABLE")
	}
	binding, err := s.Store.MoveBinding(ctx, ref.JobID)
	if errors.Is(err, os.ErrNotExist) || err == nil && binding.DelegationID != ref.DelegationID {
		return fail(CodeOperationNotFound)
	}
	if err != nil {
		return jobError(err)
	}
	if binding.CleanupDone {
		return nil
	}
	if binding.Refusal == "" {
		if err = s.ended(ctx, ref, binding.CancelRequested && binding.Settled); err != nil {
			return err
		}
	}
	bindings, err := s.Store.MoveBindings(ctx)
	if err != nil {
		return jobError(err)
	}
	for _, other := range bindings {
		if other.JobID != binding.JobID && other.Source == binding.Source && !other.Settled && !other.CleanupDone && other.Refusal == "" {
			return fail("STAGING_IN_USE")
		}
	}
	var in MoveParams
	if json.Unmarshal(binding.Params, &in) != nil {
		return fail("jobs_unavailable")
	}
	if err = cleanupSource(s.StagingDir, in.Staging, binding.Source); err != nil {
		return err
	}
	if err = s.Store.FinishMoveMirror(ctx, binding.JobID, true, ""); err != nil {
		return jobError(err)
	}
	return nil
}

// ended reports nil when the executor says the operation stopped without a
// commit. settled accepts an operation the executor no longer knows whose
// cancel the SDK already settled (forwardCancel).
func (s *Service) ended(ctx context.Context, ref OperationRef, settled bool) error {
	out, err := s.OperationStatus(ctx, nil, ref)
	if errorCode(err) == CodeOperationNotFound && settled {
		return nil
	}
	if err != nil {
		return err
	}
	switch out.State {
	case "canceled", "interrupted", "committed":
		return nil
	case "uncertain":
		return fail("uncertain")
	}
	return fail("operation-active")
}
