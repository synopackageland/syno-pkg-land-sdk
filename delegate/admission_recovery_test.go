package delegate

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// dedupExecutor models the protected receiver: one operation per job, and an
// exact repeated admission returns that operation instead of writing again.
type dedupExecutor struct {
	mu      sync.Mutex
	ops     map[string]OperationResult
	params  map[string]MoveParams
	moves   int
	created int
	cancels int
	// admit runs outside the lock before validation completes. It returns
	// whether the operation is created and the reply the caller observes.
	admit func(context.Context, MoveParams) (bool, error)
}

func newDedupExecutor() *dedupExecutor {
	return &dedupExecutor{ops: map[string]OperationResult{}, params: map[string]MoveParams{}}
}

func (e *dedupExecutor) Call(ctx context.Context, op string, _ map[string]string, in, out any) error {
	if ctx.Err() != nil {
		return fail("canceled")
	}
	switch op {
	case "files.moveFromStaging":
		p := in.(MoveParams)
		e.mu.Lock()
		e.moves++
		old, ok := e.ops[p.JobID]
		original, admit := e.params[p.JobID], e.admit
		e.mu.Unlock()
		if ok {
			if old.State == "canceled" && original == (MoveParams{}) {
				return encodeOperation(old, out) // canceled before admission
			}
			if original != p {
				return fail("operation-conflict")
			}
			return encodeOperation(old, out)
		}
		create, reply := true, error(nil)
		if admit != nil {
			create, reply = admit(ctx, p)
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		result, ok := e.ops[p.JobID]
		if create && !ok {
			result = OperationResult{JobID: p.JobID, State: "queued"}
			e.ops[p.JobID], e.params[p.JobID] = result, p
			e.created++
		}
		if reply != nil {
			return reply
		}
		return encodeOperation(result, out)
	case "files.operationStatus":
		ref := in.(OperationRef)
		e.mu.Lock()
		defer e.mu.Unlock()
		result, ok := e.ops[ref.JobID]
		if !ok {
			return fail("operation-not-found")
		}
		return encodeOperation(result, out)
	case "files.cancelOperation":
		ref := in.(OperationRef)
		e.mu.Lock()
		defer e.mu.Unlock()
		e.cancels++
		result, ok := e.ops[ref.JobID]
		switch {
		case !ok:
			result = OperationResult{JobID: ref.JobID, State: "canceled", Code: "OPERATION_CANCELED", Reason: "operation-canceled-by-user"}
		case result.State == "queued":
			result.State, result.Code, result.Reason = "canceled", "OPERATION_CANCELED", "operation-canceled-by-user"
		}
		e.ops[ref.JobID] = result
		return encodeOperation(result, out)
	}
	return fail("unsupported")
}

func (e *dedupExecutor) counts() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.moves, e.created
}

func encodeOperation(result OperationResult, out any) error {
	data, _ := json.Marshal(result)
	return json.Unmarshal(data, out)
}

type testClock struct{ at time.Time }

func (c *testClock) now() time.Time { return c.at }

func recoveringService(t *testing.T) (*Service, MoveParams, *dedupExecutor, *testClock) {
	t.Helper()
	s, in := stagedService(t)
	e := newDedupExecutor()
	clock := &testClock{at: time.Unix(1_800_000_000, 0)}
	s.Client, s.now = e, clock.now
	return s, in, e, clock
}

// restarted models a new process over the same durable mirror directory.
func restarted(s *Service, client ExecutorClient, clock *testClock) *Service {
	return &Service{Client: client, Store: background.NewStore(filepath.Dir(s.StagingDir) + "/background"), StagingDir: s.StagingDir, Lifetime: s.Lifetime, Operations: s.Operations, now: clock.now}
}

// reserveWithoutSending leaves the durable mirror a crash before transport
// would leave: reserved with the original params and pinned source.
func reserveWithoutSending(t *testing.T, s *Service, in MoveParams) {
	t.Helper()
	source, err := pinSource(s.StagingDir, in.Staging, in.Size)
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(in)
	if _, fresh, err := s.Store.ReserveMove(context.Background(), background.MoveBinding{JobID: in.JobID, DelegationID: in.DelegationID, Params: params, Source: source, Phase: background.MoveReserved}); err != nil || !fresh {
		t.Fatal(fresh, err)
	}
}

func TestUnconnectedAdmissionIsSentByTheNextSameRefCall(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	dir, err := os.MkdirTemp("/tmp", "t0203-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s.Client = &Client{Socket: filepath.Join(dir, "absent.sock"), Timeout: time.Second}
	_, err = s.MoveFromStaging(context.Background(), in)
	code(t, err, "EXECUTOR_UNAVAILABLE")
	s.Client = e
	for i := 0; i < 3; i++ {
		out, err := s.MoveFromStaging(context.Background(), in)
		if err != nil || out.JobID != in.JobID || out.State != "queued" {
			t.Fatal(i, out, err)
		}
	}
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal("admission not recovered exactly once", moves, created)
	}
}

func TestCrashBeforeTransportIsAdmittedAfterRestart(t *testing.T) {
	s, in, e, clock := recoveringService(t)
	reserveWithoutSending(t, s, in)
	out, err := restarted(s, e, clock).MoveFromStaging(context.Background(), in)
	if err != nil || out.State != "queued" {
		t.Fatal(out, err)
	}
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal(moves, created)
	}
}

func TestCrashDuringTransportResendsOnlyAfterBackoff(t *testing.T) {
	s, in, e, clock := recoveringService(t)
	s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, _, _ any) error {
		panic("injected process exit inside transport")
	})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("missing crash injection")
			}
		}()
		_, _ = s.MoveFromStaging(context.Background(), in)
	}()
	next := restarted(s, e, clock)
	_, err := next.MoveFromStaging(context.Background(), in)
	code(t, err, "ADMISSION_PENDING")
	if moves, _ := e.counts(); moves != 0 {
		t.Fatal("resent before backoff", moves)
	}
	clock.at = clock.at.Add(admissionBackoff(1))
	if out, err := next.MoveFromStaging(context.Background(), in); err != nil || out.State != "queued" {
		t.Fatal(out, err)
	}
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal(moves, created)
	}
}

func TestAdmissionAbortedDuringValidation(t *testing.T) {
	for _, admitted := range []bool{true, false} {
		t.Run(map[bool]string{true: "executor-admitted-after-caller-timeout", false: "never-admitted"}[admitted], func(t *testing.T) {
			s, in, e, clock := recoveringService(t)
			// The caller's own deadline ends while the executor is validating.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e.admit = func(ctx context.Context, _ MoveParams) (bool, error) {
				cancel()
				<-ctx.Done()
				return admitted, fail("EXECUTOR_TIMEOUT")
			}
			_, err := s.MoveFromStaging(ctx, in)
			if err == nil {
				t.Fatal("aborted admission reported success")
			}
			e.admit = nil
			clock.at = clock.at.Add(admissionBackoff(1))
			out, err := s.MoveFromStaging(context.Background(), in)
			if err != nil || out.State != "queued" {
				t.Fatal(out, err)
			}
			moves, created := e.counts()
			if created != 1 || (admitted && moves != 1) || (!admitted && moves != 2) {
				t.Fatal(moves, created)
			}
		})
	}
}

func TestLostAdmissionResponseFindsTheOriginalOperation(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	e.admit = func(context.Context, MoveParams) (bool, error) { return true, fail("EXECUTOR_PROTOCOL") }
	out, err := s.MoveFromStaging(context.Background(), in)
	if err != nil || out.State != "queued" {
		t.Fatal(out, err)
	}
	e.admit = nil
	for i := 0; i < 2; i++ {
		if out, err = s.MoveFromStaging(context.Background(), in); err != nil || out.State != "queued" {
			t.Fatal(out, err)
		}
	}
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal(moves, created)
	}
	binding, err := s.Store.MoveBinding(context.Background(), in.JobID)
	if err != nil || binding.Phase != background.MoveAccepted {
		t.Fatal("found operation not recorded as accepted", binding, err)
	}
}

func TestCompetingRecoveryCallsSendOneAdmission(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	reserveWithoutSending(t, s, in)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.MoveFromStaging(context.Background(), in)
			if err != nil && errorCode(err) != "ADMISSION_PENDING" {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal(moves, created)
	}
}

func TestCommittedOperationIsNeverAdmittedAgain(t *testing.T) {
	s, in, e, clock := recoveringService(t)
	e.ops[in.JobID] = OperationResult{JobID: in.JobID, State: "committed", Receipt: &Receipt{Path: "/home/a", Size: 3}}
	e.params[in.JobID] = in
	reserveWithoutSending(t, s, in)
	// A sent-phase mirror queries first; the committed original is returned.
	sent := restarted(s, e, clock)
	if _, _, err := sent.Store.ClaimAdmission(context.Background(), in.JobID, clock.now(), maxMoveAdmissions, admissionBackoff); err != nil {
		t.Fatal(err)
	}
	out, err := sent.MoveFromStaging(context.Background(), in)
	if err != nil || out.State != "committed" {
		t.Fatal(out, err)
	}
	if _, err = os.Stat(filepath.Join(s.StagingDir, in.Staging)); !os.IsNotExist(err) {
		t.Fatal("committed source not cleaned", err)
	}
	// Even if executor history were lost, a cleaned committed mirror is final.
	delete(e.ops, in.JobID)
	clock.at = clock.at.Add(time.Hour)
	_, err = sent.MoveFromStaging(context.Background(), in)
	code(t, err, "operation-not-found")
	if moves, _ := e.counts(); moves != 0 {
		t.Fatal("committed operation admitted again", moves)
	}
}

func TestAcceptedOperationMissingIsNotReadmitted(t *testing.T) {
	s, in, e, clock := recoveringService(t)
	if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	delete(e.ops, in.JobID)
	clock.at = clock.at.Add(time.Hour)
	_, err := s.MoveFromStaging(context.Background(), in)
	code(t, err, "operation-not-found")
	if moves, _ := e.counts(); moves != 1 {
		t.Fatal("accepted operation admitted again", moves)
	}
}

func TestTerminalRefusalsAreNeverRevived(t *testing.T) {
	for _, refusal := range []string{"APP_DENIED", "DELEGATION_DENIED", "operation-conflict", "disabled", "SCOPE_DENIED"} {
		t.Run(refusal, func(t *testing.T) {
			s, in, e, clock := recoveringService(t)
			reserveWithoutSending(t, s, in)
			e.admit = func(context.Context, MoveParams) (bool, error) { return false, fail(refusal) }
			_, err := s.MoveFromStaging(context.Background(), in)
			code(t, err, refusal)
			e.admit = nil
			clock.at = clock.at.Add(time.Hour)
			for i := 0; i < 2; i++ {
				_, err = restarted(s, e, clock).MoveFromStaging(context.Background(), in)
				code(t, err, refusal)
			}
			if moves, created := e.counts(); moves != 1 || created != 0 {
				t.Fatal(moves, created)
			}
		})
	}
}

func TestTransientRefusalsKeepTheReferenceRecoverable(t *testing.T) {
	for _, refusal := range []string{"EXECUTOR_PAUSED", "EXECUTOR_STARTING", "queue-full"} {
		t.Run(refusal, func(t *testing.T) {
			s, in, e, clock := recoveringService(t)
			e.admit = func(context.Context, MoveParams) (bool, error) { return false, fail(refusal) }
			for i := 0; i < maxMoveAdmissions+2; i++ {
				_, err := s.MoveFromStaging(context.Background(), in)
				code(t, err, refusal)
				clock.at = clock.at.Add(maxAdmissionBackoff)
			}
			e.admit = nil
			if out, err := s.MoveFromStaging(context.Background(), in); err != nil || out.State != "queued" {
				t.Fatal("proven refusal spent the resend bound", out, err)
			}
			if _, created := e.counts(); created != 1 {
				t.Fatal(created)
			}
		})
	}
}

func TestAmbiguousResendsAreBoundedWithBackoff(t *testing.T) {
	s, in, e, clock := recoveringService(t)
	e.admit = func(context.Context, MoveParams) (bool, error) { return false, fail("EXECUTOR_TIMEOUT") }
	for attempt := 1; attempt <= maxMoveAdmissions; attempt++ {
		_, err := s.MoveFromStaging(context.Background(), in)
		code(t, err, "ADMISSION_PENDING")
		if moves, _ := e.counts(); moves != attempt {
			t.Fatal("attempt", attempt, moves)
		}
		clock.at = clock.at.Add(admissionBackoff(attempt) - time.Nanosecond)
		_, err = s.MoveFromStaging(context.Background(), in)
		if attempt < maxMoveAdmissions {
			code(t, err, "ADMISSION_PENDING")
		} else {
			code(t, err, "ADMISSION_EXHAUSTED")
		}
		if moves, _ := e.counts(); moves != attempt {
			t.Fatal("resent inside backoff", attempt, moves)
		}
		clock.at = clock.at.Add(time.Nanosecond)
	}
	clock.at = clock.at.Add(24 * time.Hour)
	_, err := s.MoveFromStaging(context.Background(), in)
	code(t, err, "ADMISSION_EXHAUSTED")
	if moves, _ := e.counts(); moves != maxMoveAdmissions {
		t.Fatal(moves)
	}
	if admissionBackoff(maxMoveAdmissions) != maxAdmissionBackoff || admissionBackoff(1) != 2*time.Second {
		t.Fatal(admissionBackoff(1), admissionBackoff(maxMoveAdmissions))
	}
}

func TestReplacedSourceIsNeverResent(t *testing.T) {
	s, in, e, clock := recoveringService(t)
	reserveWithoutSending(t, s, in)
	source := filepath.Join(s.StagingDir, in.Staging)
	os.Rename(source, source+".old")
	os.WriteFile(source, []byte("new"), 0600)
	_, err := restarted(s, e, clock).MoveFromStaging(context.Background(), in)
	code(t, err, "STAGING_CHANGED")
	if moves, _ := e.counts(); moves != 0 {
		t.Fatal("replaced source admitted", moves)
	}
}

func TestSweepAdmitsAnUnsentReservedMove(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	reserveWithoutSending(t, s, in)
	if err := s.SweepMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal(moves, created)
	}
}
