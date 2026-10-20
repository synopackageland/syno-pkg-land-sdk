package delegate

import (
	"context"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A producer must know from the code alone whether repeating the same params
// is safe: retryable codes never wrote anything a retry could duplicate, and
// terminal codes need the package or administrator to repair the spool first.
func TestJobErrorSeparatesRetryableFromTerminalSpoolFailures(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{background.ErrBusy, "jobs_busy"},
		{context.DeadlineExceeded, "canceled"},
		{context.Canceled, "canceled"},
		{fmt.Errorf("journal: %w", context.DeadlineExceeded), "canceled"},
		{&fs.PathError{Op: "write", Path: "journal.json", Err: syscall.EIO}, "jobs_unavailable"},
		{background.ErrUnsafeSpool, "spool_unsafe"},
		{background.ErrJournalUnreadable, "journal_corrupt"},
		{background.ErrJournalSchema, "journal_corrupt"},
	} {
		code(t, jobError(tc.err), tc.want)
	}
}

// holdJournal keeps the spool lock as another process would, until release.
func holdJournal(t *testing.T, s *Service) (release func()) {
	t.Helper()
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(s.StagingDir), "background", "lock"), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	return func() { unix.Flock(int(lock.Fd()), unix.LOCK_UN); lock.Close() }
}

func commit(e *dedupExecutor, in MoveParams) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ops[in.JobID] = OperationResult{JobID: in.JobID, State: "committed", Receipt: &Receipt{Path: "/home/" + in.Name, Size: in.Size}}
}

// a caller deadline spent waiting for the journal is retryable, and
// the same params later converge on the original committed operation.
func TestJournalDeadlineIsRetryableAndConvergesOnTheOriginalOperation(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	if out, err := s.MoveFromStaging(context.Background(), in); err != nil || out.State != "queued" {
		t.Fatal(out, err)
	}
	release := holdJournal(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := s.MoveFromStaging(ctx, in)
	cancel()
	release()
	code(t, err, "canceled")
	commit(e, in)
	if out, err := s.MoveFromStaging(context.Background(), in); err != nil || out.State != "committed" {
		t.Fatal(out, err)
	}
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal("same tuple admitted again", moves, created)
	}
}

func TestFirstAdmissionUnderJournalDeadlineSendsNothingUntilRetried(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	if _, err := s.Store.PendingMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	release := holdJournal(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := s.MoveFromStaging(ctx, in)
	cancel()
	release()
	code(t, err, "canceled")
	if moves, _ := e.counts(); moves != 0 {
		t.Fatal("admitted without a durable reservation", moves)
	}
	if out, err := s.MoveFromStaging(context.Background(), in); err != nil || out.State != "queued" {
		t.Fatal(out, err)
	}
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal(moves, created)
	}
}

// A busy journal must not hide a committed receipt: the same ref answers from
// the executor's original operation, read-only, without a new admission.
func TestCommittedRefAnswersItsReceiptWhileTheJournalIsBusy(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	commit(e, in)
	s.Store = background.NewRequestStore(filepath.Join(filepath.Dir(s.StagingDir), "background"))
	release := holdJournal(t, s)
	defer release()
	if out, err := s.MoveFromStaging(context.Background(), in); err != nil || out.State != "committed" {
		t.Fatal(out, err)
	}
	ref := OperationRef{in.DelegationID, in.JobID}
	if out, err := s.OperationStatus(context.Background(), nil, ref); err != nil || out.State != "committed" || out.Receipt == nil || out.Receipt.Size != in.Size {
		t.Fatal(out, err)
	}
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal(moves, created)
	}
}

func TestUnknownRefStaysRetryableWhileTheJournalIsBusy(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	if _, err := s.Store.PendingMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Store = background.NewRequestStore(filepath.Join(filepath.Dir(s.StagingDir), "background"))
	release := holdJournal(t, s)
	defer release()
	_, err := s.MoveFromStaging(context.Background(), in)
	code(t, err, "jobs_busy")
	if moves, _ := e.counts(); moves != 0 {
		t.Fatal(moves)
	}
}
