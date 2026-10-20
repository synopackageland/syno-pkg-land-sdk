package delegate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const MaxMoveBytes int64 = 16 << 30

// An operation the executor does not know is admitted again at most
// maxMoveAdmissions times, waiting admissionBackoff after each such send.
const (
	maxMoveAdmissions     = 10
	firstAdmissionBackoff = 2 * time.Second
	maxAdmissionBackoff   = 5 * time.Minute
)

func admissionBackoff(attempts int) time.Duration {
	delay := firstAdmissionBackoff
	for ; attempts > 1 && delay < maxAdmissionBackoff; attempts-- {
		delay *= 2
	}
	return min(delay, maxAdmissionBackoff)
}

type MoveParams struct {
	DelegationID string `json:"delegation_id"`
	JobID        string `json:"job_id"`
	Staging      string `json:"staging"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	// Conflict is "fail" (the default when empty), "overwrite" or "rename".
	Conflict string `json:"conflict,omitempty"`
}

func (r *Receipt) UnmarshalJSON(data []byte) error {
	var wire struct {
		Path *string `json:"path"`
		Size *int64  `json:"size"`
	}
	if json.Unmarshal(data, &wire) != nil || wire.Path == nil || wire.Size == nil {
		return fail(CodeExecutorProtocol)
	}
	r.Path, r.Size = *wire.Path, *wire.Size
	return nil
}

var operationToken = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func validRef(in OperationRef) bool {
	return operationToken.MatchString(in.DelegationID) && operationToken.MatchString(in.JobID)
}
func validMove(in MoveParams) bool {
	return validRef(OperationRef{in.DelegationID, in.JobID}) && validStaging(in.Staging) && validDestination(in.Name) && (in.Conflict == "" || in.Conflict == "fail" || in.Conflict == "overwrite" || in.Conflict == "rename") && in.Size >= 0 && in.Size <= MaxMoveBytes
}

// validDestination accepts a file name or folders below the destination, such
// as "Album/CD1/01.flac". The executor applies the full folder rules and limits.
func validDestination(name string) bool {
	if len(name) > 1024 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if !validName(part) {
			return false
		}
	}
	return true
}

// maxRenameAttempts matches the executor: "a (1).txt" to "a (99).txt".
const maxRenameAttempts = 99

// receiptMatches reports whether a committed path is the requested name, or
// with conflict "rename" one of its DSM-style "name (n).ext" alternatives.
func receiptMatches(receipt string, in MoveParams) bool {
	if strings.HasSuffix(receipt, "/"+in.Name) {
		return true
	}
	for n := 1; in.Conflict == "rename" && n <= maxRenameAttempts; n++ {
		if strings.HasSuffix(receipt, "/"+renameCandidate(in.Name, n)) {
			return true
		}
	}
	return false
}
func renameCandidate(name string, n int) string {
	dir, leaf := "", name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		dir, leaf = name[:i+1], name[i+1:]
	}
	stem, ext := leaf, ""
	if i := strings.LastIndex(leaf, "."); i > 0 {
		stem, ext = leaf[:i], leaf[i:]
	}
	return dir + stem + " (" + strconv.Itoa(n) + ")" + ext
}
func validateMove(in MoveParams) error {
	if in.Size < 0 || in.Size > MaxMoveBytes {
		return fail("invalid-size")
	}
	if !validMove(in) {
		return fail(CodeInvalidRequest)
	}
	return nil
}

func validOperation(out OperationResult, ref OperationRef) bool {
	if out.JobID != ref.JobID || (out.Code != "" && !stableCode.MatchString(out.Code)) || (out.Reason != "" && !stableCode.MatchString(out.Reason)) {
		return false
	}
	switch out.State {
	case "queued", "copying", "canceled", "interrupted", "uncertain":
		return out.Receipt == nil
	case "committed":
		return out.Receipt != nil && validPath(out.Receipt.Path) && out.Receipt.Path != "/" && out.Receipt.Size >= 0 && out.Receipt.Size <= MaxMoveBytes
	default:
		return false
	}
}

// MoveFromStaging is the package backend/daemon completion hook. It never
// accepts a destination path, owner or UID, and does not require a live session.
type OperationAdmission struct {
	JobID string `json:"job_id"`
	State string `json:"state"`
}

func (s *Service) admitStaging(ctx context.Context, auth map[string]string, in MoveParams) (OperationAdmission, error) {
	out, err := s.moveFromStaging(ctx, auth, in)
	if err != nil {
		return OperationAdmission{}, err
	}
	return OperationAdmission{JobID: out.JobID, State: out.State}, nil
}

func (s *Service) MoveFromStaging(ctx context.Context, in MoveParams) (OperationAdmission, error) {
	return s.admitStaging(ctx, nil, in)
}
func (s *Service) moveFromStaging(ctx context.Context, auth map[string]string, in MoveParams) (OperationResult, error) {
	if background.RefuseRoot(os.Geteuid()) != nil {
		return OperationResult{}, fail("STAGING_UNAVAILABLE")
	}
	if s.Lifetime != "until-revoked" || !contains(s.Operations, "files.moveFromStaging") {
		return OperationResult{}, fail("unsupported")
	}
	if err := validateMove(in); err != nil {
		return OperationResult{}, err
	}
	if s.Store == nil {
		return OperationResult{}, fail("STAGING_UNAVAILABLE")
	}
	params, _ := json.Marshal(in)
	ref := OperationRef{in.DelegationID, in.JobID}
	binding, err := s.Store.MoveBinding(ctx, in.JobID)
	fresh := false
	if errors.Is(err, os.ErrNotExist) {
		source, err := pinSource(s.StagingDir, in.Staging, in.Size)
		if err != nil {
			return OperationResult{}, err
		}
		binding, fresh, err = s.Store.ReserveMove(ctx, background.MoveBinding{JobID: in.JobID, DelegationID: in.DelegationID, Params: params, Source: source, Phase: background.MoveReserved})
		if err != nil {
			if errors.Is(err, background.ErrKeyConflict) {
				return OperationResult{}, fail(CodeOperationConflict)
			}
			return s.journalUnavailable(ctx, auth, ref, err)
		}
	} else if err != nil {
		return s.journalUnavailable(ctx, auth, ref, err)
	}
	if binding.DelegationID != in.DelegationID || !bytes.Equal(binding.Params, params) {
		return OperationResult{}, fail(CodeOperationConflict)
	}
	if binding.Refusal != "" {
		return OperationResult{}, fail(binding.Refusal)
	}
	if binding.CancelRequested {
		// A canceled reference is never admitted again; the cancel is forwarded.
		return s.forwardCancel(ctx, auth, ref)
	}
	// A request that may have been sent is looked up first. Only an operation the
	// executor does not know is admitted again with the original params; the
	// executor deduplicates the exact tuple, so a racing original is not copied twice.
	queried := false
	if !fresh && binding.Phase != background.MoveReserved {
		out, err := s.OperationStatus(ctx, auth, ref)
		if errorCode(err) != CodeOperationNotFound || binding.Phase == background.MoveAccepted || binding.Settled || binding.CleanupDone {
			return out, err
		}
		queried = true
	}
	if !fresh {
		if source, err := pinSource(s.StagingDir, in.Staging, in.Size); err != nil || source != binding.Source {
			return OperationResult{}, fail("STAGING_CHANGED")
		}
	}
	binding, claimed, err := s.Store.ClaimAdmission(ctx, in.JobID, s.clockNow(), maxMoveAdmissions, admissionBackoff)
	if err != nil {
		return s.journalUnavailable(ctx, auth, ref, err)
	}
	if !claimed {
		return s.unclaimedAdmission(ctx, auth, ref, binding, queried)
	}
	return s.sendAdmission(ctx, auth, in)
}

// journalUnavailable answers a same-ref call the journal could not serve. An
// operation the executor already has is reported read-only; anything else keeps
// the retryable store code, so nothing is admitted without a durable reservation.
// An unsafe or corrupt spool stays terminal.
func (s *Service) journalUnavailable(ctx context.Context, auth map[string]string, ref OperationRef, err error) (OperationResult, error) {
	err = jobError(err)
	if code := errorCode(err); code != "jobs_busy" && code != "canceled" && code != "jobs_unavailable" {
		return OperationResult{}, err
	}
	if out, found := s.OperationStatus(ctx, auth, ref); found == nil {
		return out, nil
	}
	return OperationResult{}, err
}

// unclaimedAdmission answers a caller that may not send now: another caller
// holds the send, its backoff has not ended, or the bound is spent.
func (s *Service) unclaimedAdmission(ctx context.Context, auth map[string]string, ref OperationRef, binding background.MoveBinding, queried bool) (OperationResult, error) {
	if binding.Refusal != "" {
		return OperationResult{}, fail(binding.Refusal)
	}
	if !queried {
		out, err := s.OperationStatus(ctx, auth, ref)
		if errorCode(err) != CodeOperationNotFound {
			return out, err
		}
	}
	if binding.Attempts >= maxMoveAdmissions {
		return OperationResult{}, fail("ADMISSION_EXHAUSTED")
	}
	return OperationResult{}, fail("ADMISSION_PENDING")
}

func (s *Service) sendAdmission(ctx context.Context, auth map[string]string, in MoveParams) (OperationResult, error) {
	ref := OperationRef{in.DelegationID, in.JobID}
	// Mirror writes survive a caller deadline that ended the request.
	persist, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	var out OperationResult
	err := s.Client.Call(ctx, "files.moveFromStaging", auth, in, &out)
	if err != nil {
		var dial *dialFailure
		code := errorCode(err)
		switch {
		case errors.As(err, &dial) || code == "EXECUTOR_PAUSED" || code == "EXECUTOR_STARTING" || code == "queue-full":
			// Proven not admitted: no connection, or refused before an operation existed.
			_ = s.Store.ReleaseAdmission(persist, in.JobID)
			return OperationResult{}, err
		case code == CodeExecutorUnavailable || code == CodeExecutorTimeout || code == CodeExecutorProtocol || code == "STORAGE_ERROR" || code == "canceled":
			// The request might have arrived; inspect only the original operation.
			out, err = s.OperationStatus(ctx, auth, ref)
			if errorCode(err) == CodeOperationNotFound {
				return OperationResult{}, fail("ADMISSION_PENDING")
			}
			return out, err
		}
		_ = s.Store.FinishMoveMirror(persist, in.JobID, false, code)
		return OperationResult{}, err
	}
	// Admission promises a reference/state only; a committed receipt is obtained
	// by status and is never invented from the requested name or size.
	if out.JobID != in.JobID {
		return OperationResult{}, fail(CodeExecutorProtocol)
	}
	_ = s.Store.AcceptMove(persist, in.JobID)
	switch out.State {
	case "queued", "copying", "canceled", "interrupted", "uncertain":
		if out.Receipt != nil {
			return OperationResult{}, fail(CodeExecutorProtocol)
		}
		return out, nil
	case "committed":
		return s.OperationStatus(ctx, auth, ref)
	default:
		return OperationResult{}, fail(CodeExecutorProtocol)
	}
}

func (s *Service) operation(ctx context.Context, auth map[string]string, ref OperationRef, op string) (OperationResult, error) {
	var out OperationResult
	if !validRef(ref) {
		return out, fail(CodeInvalidRequest)
	}
	err := s.Client.Call(ctx, op, auth, ref, &out)
	if err != nil {
		return OperationResult{}, err
	}
	if !validOperation(out, ref) {
		return OperationResult{}, fail(CodeExecutorProtocol)
	}
	if s.Store != nil {
		// A found operation is never admitted again, even if history is later lost.
		_ = s.Store.AcceptMove(ctx, ref.JobID)
	}
	if out.State == "committed" && s.Store != nil {
		binding, err := s.Store.MoveBinding(ctx, ref.JobID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		if err == nil && binding.DelegationID == ref.DelegationID && !binding.CleanupDone {
			var input MoveParams
			if json.Unmarshal(binding.Params, &input) != nil || input.JobID != ref.JobID || out.Receipt.Size != input.Size || !receiptMatches(out.Receipt.Path, input) {
				return OperationResult{}, fail(CodeExecutorProtocol)
			}
			// A cleanup failure leaves source and mirror available for package retry.
			// Committed remains committed and its protected receipt remains the result.
			if cleanupSource(s.StagingDir, input.Staging, binding.Source) == nil {
				_ = s.Store.FinishMoveMirror(ctx, ref.JobID, true, "")
			}
		}
	}
	if s.Store != nil && (out.State == "canceled" || out.State == "interrupted" || out.State == "uncertain") {
		_ = s.Store.SettleMoveMirror(ctx, ref.JobID)
	}
	return out, nil
}
func (s *Service) OperationStatus(ctx context.Context, auth map[string]string, ref OperationRef) (OperationResult, error) {
	return s.operation(ctx, auth, ref, "files.operationStatus")
}

// CancelOperation acknowledges a copy in flight with state copying and code
// CANCEL_REQUESTED; poll OperationStatus for the terminal state. A producer
// (no session) cancel is made durable in the mirror first, so the SDK's
// admission recovery forwards it instead of sending the admission. A mirror
// write failure does not hold the cancel back: the executor stays authoritative.
func (s *Service) CancelOperation(ctx context.Context, auth map[string]string, ref OperationRef) (OperationResult, error) {
	if len(auth) == 0 && s.Store != nil && validRef(ref) {
		_ = s.Store.RequestMoveCancel(ctx, ref.JobID, ref.DelegationID)
	}
	return s.forwardCancel(ctx, auth, ref)
}

// forwardCancel sends a cancel to the executor. The executor answers a producer
// cancel with operation-not-found only when it knows neither the operation nor
// its grant, for example after an uninstall purge: no admission can succeed
// without the grant, so the durable cancel is settled here as canceled.
func (s *Service) forwardCancel(ctx context.Context, auth map[string]string, ref OperationRef) (OperationResult, error) {
	out, err := s.operation(ctx, auth, ref, "files.cancelOperation")
	if len(auth) != 0 || s.Store == nil || errorCode(err) != CodeOperationNotFound {
		return out, err
	}
	binding, readErr := s.Store.MoveBinding(ctx, ref.JobID)
	if readErr != nil || binding.DelegationID != ref.DelegationID || !binding.CancelRequested {
		return out, err
	}
	_ = s.Store.SettleMoveMirror(ctx, ref.JobID)
	return OperationResult{JobID: ref.JobID, State: "canceled", Code: CodeOperationCanceled, Reason: ReasonOperationCanceledByUser}, nil
}

// SweepMoves queries mirrors using the package peer identity after logout/boot.
// An operation the executor does not know is recovered like a repeated
// MoveFromStaging call. It never revokes a reusable destination grant.
func (s *Service) SweepMoves(ctx context.Context) error {
	if s.Store == nil {
		return nil
	}
	bindings, err := s.Store.PendingMoves(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, binding := range bindings {
		var in MoveParams
		var out OperationResult
		if json.Unmarshal(binding.Params, &in) == nil {
			out, err = s.moveFromStaging(ctx, nil, in)
		} else {
			out, err = s.OperationStatus(ctx, nil, OperationRef{binding.DelegationID, binding.JobID})
		}
		if err == nil && out.State == "committed" {
			mirror, readErr := s.Store.MoveBinding(ctx, binding.JobID)
			if readErr != nil || !mirror.CleanupDone {
				err = fail("STAGING_CLEANUP_PENDING")
			}
		}
		result = errors.Join(result, err)
	}
	return result
}
