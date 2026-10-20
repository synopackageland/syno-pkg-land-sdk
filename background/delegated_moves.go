package background

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// StagingIdentity identifies a package-owned source for cleanup only. It is
// never a grant, a UID selector for destination writes, or protected authority.
type StagingIdentity struct {
	Device, Inode, ParentDevice, ParentInode, RootDevice, RootInode uint64
	UID                                                             uint32
	Size, ModifiedNS                                                int64
}

type MoveBinding struct {
	JobID        string          `json:"job_id"`
	DelegationID string          `json:"delegation_id"`
	Params       json.RawMessage `json:"params"`
	Source       StagingIdentity `json:"source"`
	Settled      bool            `json:"settled,omitempty"`
	CleanupDone  bool            `json:"cleanup_done,omitempty"`
	Refusal      string          `json:"refusal,omitempty"`
	// Phase records how far admission got. Older mirrors have none and are
	// treated as possibly sent. Attempts counts sends that may have arrived.
	Phase       string `json:"phase,omitempty"`
	Attempts    int    `json:"attempts,omitempty"`
	NextAttempt int64  `json:"next_attempt,omitempty"`
	// CancelRequested is the producer's durable cancel: it is forwarded until
	// the executor answers, and admission is never sent again.
	CancelRequested bool `json:"cancel_requested,omitempty"`
}

const (
	MoveReserved = "reserved" // durable before transport; no request sent
	MoveSent     = "sent"     // a request may have reached the executor
	MoveAccepted = "accepted" // the executor returned the operation
)

const maxMoveMirrorBytes = 10 << 20

// maxFinishedMoves bounds the history of finished moves, those whose staging
// source is gone (committed or discarded). Their status stays the executor's;
// a mirror that may still copy or still holds its source is never dropped.
const maxFinishedMoves = 64

func moveBindingName(id string) string { return "move-" + sha256hex([]byte(id)) + ".json" }

func readMoveBinding(root *os.Root, id string) (MoveBinding, error) {
	var out MoveBinding
	f, err := root.OpenFile(moveBindingName(id), os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return out, err
	}
	defer f.Close()
	if err = checkPrivate(f); err != nil {
		return out, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(data) > 16384 || json.Unmarshal(data, &out) != nil || out.JobID != id || out.DelegationID == "" || !json.Valid(out.Params) {
		return MoveBinding{}, ErrInvalid
	}
	return out, nil
}

func (s *Store) MoveBinding(ctx context.Context, id string) (MoveBinding, error) {
	var out MoveBinding
	err := s.withJournal(ctx, func(root *os.Root, _ *journal) (bool, error) {
		var err error
		out, err = readMoveBinding(root, id)
		return false, err
	})
	return out, err
}

// ReserveMove fsyncs a mirror before admission. A process crash or ambiguous
// response leaves this reference for a status query first; only an operation
// the executor does not know is admitted again, through ClaimAdmission.
// A short spool lock covers metadata only, not a socket call or file copy.
func (s *Store) ReserveMove(ctx context.Context, in MoveBinding) (MoveBinding, bool, error) {
	out := in
	fresh := false
	err := s.withJournal(ctx, func(root *os.Root, _ *journal) (bool, error) {
		existing, err := readMoveBinding(root, in.JobID)
		if err == nil {
			if existing.DelegationID != in.DelegationID || !bytes.Equal(existing.Params, in.Params) {
				return false, ErrKeyConflict
			}
			out = existing
			return false, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		dir, err := root.Open(".")
		if err != nil {
			return false, err
		}
		defer dir.Close()
		entries, err := dir.ReadDir(-1)
		if err != nil {
			return false, err
		}
		var used int64
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "move-") && strings.HasSuffix(e.Name(), ".json") {
				info, err := e.Info()
				if err != nil {
					return false, err
				}
				used += info.Size()
			}
		}
		// Completed/uncertain mirrors are retained for local replay prevention.
		// The protected executor owns the authoritative operation/history quota.
		encoded, err := json.Marshal(in)
		if err != nil || len(encoded) > 16384 {
			return false, ErrInvalid
		}
		if used+int64(len(encoded)) > maxMoveMirrorBytes {
			return false, ErrQuota
		}
		if err = writeAtomic(root, moveBindingName(in.JobID), in); err != nil {
			return false, err
		}
		fresh = true
		// Retention is housekeeping: a failure keeps history, never the save.
		_ = pruneFinishedMoves(root, entries)
		return false, nil
	})
	return out, fresh, err
}

// pruneFinishedMoves removes the oldest finished mirrors among entries beyond
// maxFinishedMoves; entries is the spool listing taken under the same lock.
func pruneFinishedMoves(root *os.Root, entries []os.DirEntry) error {
	type finished struct {
		name     string
		modified time.Time
	}
	var done []finished
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "move-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		f, err := root.OpenFile(e.Name(), os.O_RDONLY|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, 16385))
		info, statErr := f.Stat()
		f.Close()
		var b MoveBinding
		if err != nil || statErr != nil || json.Unmarshal(data, &b) != nil || e.Name() != moveBindingName(b.JobID) {
			continue
		}
		if b.CleanupDone {
			done = append(done, finished{e.Name(), info.ModTime()})
		}
	}
	if len(done) <= maxFinishedMoves {
		return nil
	}
	sort.Slice(done, func(i, j int) bool {
		if !done[i].modified.Equal(done[j].modified) {
			return done[i].modified.Before(done[j].modified)
		}
		return done[i].name < done[j].name
	})
	for _, old := range done[:len(done)-maxFinishedMoves] {
		if err := root.Remove(old.name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// updateMoveBinding rewrites one existing mirror under the spool lock when
// change reports a modification.
func (s *Store) updateMoveBinding(ctx context.Context, id string, change func(*MoveBinding) bool) error {
	return s.withJournal(ctx, func(root *os.Root, _ *journal) (bool, error) {
		in, err := readMoveBinding(root, id)
		if err != nil || !change(&in) {
			return false, err
		}
		return false, writeAtomic(root, moveBindingName(id), in)
	})
}

// ClaimAdmission durably records one send before it happens. Concurrent
// callers serialize here: a claim is refused for accepted, settled, refused
// or exhausted mirrors, and before the backoff of the previous send ends.
func (s *Store) ClaimAdmission(ctx context.Context, id string, now time.Time, limit int, backoff func(int) time.Duration) (MoveBinding, bool, error) {
	var out MoveBinding
	claimed := false
	err := s.updateMoveBinding(ctx, id, func(in *MoveBinding) bool {
		out = *in
		if in.Phase == MoveAccepted || in.Settled || in.CleanupDone || in.Refusal != "" || in.CancelRequested || in.Attempts >= limit || now.UnixNano() < in.NextAttempt {
			return false
		}
		in.Phase, in.Attempts = MoveSent, in.Attempts+1
		in.NextAttempt = now.Add(backoff(in.Attempts)).UnixNano()
		out, claimed = *in, true
		return true
	})
	return out, claimed, err
}

// ReleaseAdmission returns a claim the executor proved it did not admit, so
// the bound counts only sends that might have created an operation.
func (s *Store) ReleaseAdmission(ctx context.Context, id string) error {
	return s.updateMoveBinding(ctx, id, func(in *MoveBinding) bool {
		if in.Phase != MoveSent {
			return false
		}
		in.Phase, in.Attempts, in.NextAttempt = MoveReserved, in.Attempts-1, 0
		return true
	})
}

// RequestMoveCancel durably records a cancel of the mirror bound to the grant.
func (s *Store) RequestMoveCancel(ctx context.Context, id, delegationID string) error {
	return s.updateMoveBinding(ctx, id, func(in *MoveBinding) bool {
		changed := in.DelegationID == delegationID && !in.CancelRequested
		in.CancelRequested = in.CancelRequested || changed
		return changed
	})
}

func (s *Store) AcceptMove(ctx context.Context, id string) error {
	return s.updateMoveBinding(ctx, id, func(in *MoveBinding) bool {
		changed := in.Phase != MoveAccepted
		in.Phase = MoveAccepted
		return changed
	})
}

func (s *Store) FinishMoveMirror(ctx context.Context, id string, cleaned bool, refusal string) error {
	return s.updateMoveBinding(ctx, id, func(in *MoveBinding) bool {
		in.CleanupDone = in.CleanupDone || cleaned
		in.Settled = in.Settled || cleaned
		if refusal != "" {
			in.Refusal = refusal
		}
		return true
	})
}

func (s *Store) moveBindings(ctx context.Context) ([]MoveBinding, error) {
	out := []MoveBinding{}
	err := s.withJournal(ctx, func(root *os.Root, _ *journal) (bool, error) {
		dir, err := root.Open(".")
		if err != nil {
			return false, err
		}
		defer dir.Close()
		entries, err := dir.ReadDir(-1)
		if err != nil {
			return false, err
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "move-") || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			// Decode the bounded record to recover its non-path job key; verify it
			// again through readMoveBinding before trusting even this local mirror.
			f, err := root.OpenFile(entry.Name(), os.O_RDONLY|unix.O_NOFOLLOW, 0)
			if err != nil {
				return false, err
			}
			err = checkPrivate(f)
			data, readErr := io.ReadAll(io.LimitReader(f, 16385))
			f.Close()
			var key struct {
				JobID string `json:"job_id"`
			}
			if err != nil || readErr != nil || len(data) > 16384 || json.Unmarshal(data, &key) != nil || entry.Name() != moveBindingName(key.JobID) {
				return false, ErrInvalid
			}
			binding, err := readMoveBinding(root, key.JobID)
			if err != nil {
				return false, err
			}
			out = append(out, binding)
		}
		return false, nil
	})
	return out, err
}

func (s *Store) PendingMoves(ctx context.Context) ([]MoveBinding, error) {
	bindings, err := s.moveBindings(ctx)
	out := []MoveBinding{}
	for _, binding := range bindings {
		if !binding.Settled && !binding.CleanupDone && binding.Refusal == "" {
			out = append(out, binding)
		}
	}
	return out, err
}

// MoveBindings includes settled/refused history. Callers must apply fresh
// owner authorization; package mirrors never confer destination authority.
func (s *Store) MoveBindings(ctx context.Context) ([]MoveBinding, error) { return s.moveBindings(ctx) }

// Settled mirrors remain available to explicit status calls and idempotency,
// but terminal operations do not make the worker poll history indefinitely.
func (s *Store) SettleMoveMirror(ctx context.Context, id string) error {
	return s.updateMoveBinding(ctx, id, func(in *MoveBinding) bool {
		in.Settled = true
		return true
	})
}
