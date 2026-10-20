package background

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const WorkerDownloadV1 = "download-to-folder-v1"

// DelegationBinding is job metadata, never a session credential. It stays until
// executor revocation succeeds, even if cancellation removes the job input.
type DelegationBinding struct {
	JobID           string    `json:"job_id"`
	ExpiresAt       time.Time `json:"expires_at,omitzero"`
	WaitingExecutor bool      `json:"waiting_executor,omitempty"`
	DelegationID    string    `json:"delegation_id"`
}

func bindingName(id string) string { return "delegate-" + id + ".json" }

// SubmitDelegated reserves a job under the spool lock, creates its grant without
// the lock, then commits the binding. Workers cannot claim a pending reservation.
func (s *Store) SubmitDelegated(ctx context.Context, req SubmitRequest, create func(string) (DelegationBinding, error), rollback func(DelegationBinding)) (Job, error) {
	if len(req.Data) == 0 || len(req.Data) > MaxInputBytes || !keyRE.MatchString(req.Key) || !validName(req.Name) || !req.Submitter.Valid() {
		return Job{}, ErrInvalid
	}
	digest := sha256hex(req.Data)
	var out Job
	reserved := false
	err := s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		for _, job := range j.Jobs {
			if job.Submitter == req.Submitter && job.Key == req.Key && !job.PurgeRequested {
				if job.InputDigest != digest || job.Worker != WorkerDownloadV1 {
					return false, ErrKeyConflict
				}
				if job.AdmissionPending {
					return false, ErrBusy
				}
				out = job
				return false, nil
			}
		}
		pending, ownerBytes := j.ownerUsage(req.Submitter)
		if pending >= MaxOwnerPending {
			return false, ErrOwnerQueueFull
		}
		if ownerBytes+int64(len(req.Data)+resultReserve) > MaxOwnerBytes {
			return false, ErrOwnerQuota
		}
		count, used := j.pending()
		if count >= MaxQueued {
			return false, ErrQueueFull
		}
		if used+int64(len(req.Data)+resultReserve) > s.limits.MaxTotalBytes {
			return false, ErrQuota
		}
		// Executor outages must not create an unbounded cleanup journal.
		dir, e := root.Open(".")
		if e != nil {
			return false, e
		}
		entries, e := dir.ReadDir(-1)
		dir.Close()
		if e != nil {
			return false, e
		}
		bindings := 0
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "delegate-") && strings.HasSuffix(entry.Name(), ".json") {
				bindings++
			}
		}
		// Pending admissions can each create one cleanup record outside this lock.
		for _, job := range j.Jobs {
			if job.AdmissionPending {
				bindings++
			}
		}
		if bindings >= MaxHistory+MaxQueued {
			return false, ErrQuota
		}

		id, e := newID()
		if e != nil {
			return false, e
		}

		if e = writeInput(root, id, req.Data); e != nil {
			return false, e
		}
		reserved = true
		out = Job{AdmissionPending: true, ID: id, Key: req.Key, Name: req.Name, State: StateQueued, Submitter: req.Submitter, Worker: WorkerDownloadV1, WorkerRevision: s.revision, InputDigest: digest, InputBytes: len(req.Data), SubmittedAt: s.now().UTC()}
		j.Jobs = append(j.Jobs, out)
		j.trim()
		return true, nil
	})
	if err != nil || !reserved {
		return out, err
	}
	binding, err := create(out.ID)
	binding.JobID = out.ID
	grant := binding.DelegationID
	if err == nil && grant == "" {
		err = ErrInvalid
	}
	if err == nil {
		err = s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
			if e := writeAtomic(root, bindingName(out.ID), binding); e != nil {
				return false, e
			}
			job := find(j, out.ID)
			if job == nil || !job.AdmissionPending || job.State != StateQueued || job.CancelRequested || job.PurgeRequested {
				return false, ErrNotCancelable
			}
			job.AdmissionPending = false
			job.DelegationID = grant
			out = *job
			return true, nil
		})
	}
	if err != nil {
		// Retain failed revocations independently of the request's canceled context.
		cleanup, cancel := context.WithTimeout(context.Background(), RequestLockTimeout)
		_ = s.withJournal(cleanup, func(root *os.Root, j *journal) (bool, error) {
			if grant != "" {
				if e := writeAtomic(root, bindingName(out.ID), binding); e != nil {
					return false, e
				}
			}
			job := find(j, out.ID)
			if job == nil || !job.AdmissionPending {
				return false, nil
			}
			job.AdmissionPending = false
			if job.State == StateQueued {
				s.finishJob(root, job, StateFailed, "delegation-uncommitted")
			}
			return true, nil
		})
		cancel()
		if grant != "" {
			rollback(binding)
		}
		return Job{}, err
	}
	return out, nil
}
func readBinding(root *os.Root, id string) (DelegationBinding, error) {
	var b DelegationBinding
	if !jobID.MatchString(id) {
		return b, ErrInvalid
	}
	f, e := root.OpenFile(bindingName(id), os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if e != nil {
		return b, e
	}
	defer f.Close()
	if e = checkPrivate(f); e != nil {
		return b, e
	}
	data, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil || len(data) > 4096 || json.Unmarshal(data, &b) != nil || b.JobID != id || b.DelegationID == "" {
		return b, ErrInvalid
	}
	return b, nil
}
func (s *Store) Delegation(ctx context.Context, id string) (DelegationBinding, error) {
	var b DelegationBinding
	e := s.withJournal(ctx, func(root *os.Root, _ *journal) (bool, error) {
		var e error
		b, e = readBinding(root, id)
		return false, e
	})
	return b, e
}

// DelegationCleanup includes orphaned grants and canceled/running jobs. The
// independent metadata record makes restart and executor outages retryable.
func (s *Store) DelegationCleanup(ctx context.Context) ([]DelegationBinding, error) {
	out := []DelegationBinding{}
	e := s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		dir, e := root.Open(".")
		if e != nil {
			return false, e
		}
		defer dir.Close()
		entries, e := dir.ReadDir(-1)
		if e != nil {
			return false, e
		}
		for _, entry := range entries {
			n := entry.Name()
			if !strings.HasPrefix(n, "delegate-") || !strings.HasSuffix(n, ".json") {
				continue
			}
			id := strings.TrimSuffix(strings.TrimPrefix(n, "delegate-"), ".json")
			b, e := readBinding(root, id)
			if e != nil {
				return false, e
			}
			job := find(j, id)
			if job == nil || job.State.terminal() || job.CancelRequested || job.PurgeRequested {
				out = append(out, b)
			}
		}
		return false, nil
	})
	return out, e
}
func (s *Store) ForgetDelegation(ctx context.Context, id string) error {
	return s.withJournal(ctx, func(root *os.Root, _ *journal) (bool, error) {
		if !jobID.MatchString(id) {
			return false, ErrInvalid
		}
		e := root.Remove(bindingName(id))
		if errors.Is(e, os.ErrNotExist) {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		return false, syncDir(root, ".")
	})
}

// SetExecutorWaiting updates only the delegation sidecar. The claimed journal
// slot remains reserved, preserving quotas and cancellation/recovery semantics.
// No journal lock survives this method, a socket call, or a retry timer.
func (s *Store) SetExecutorWaiting(ctx context.Context, id string, waiting bool) error {
	return s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		job := find(j, id)
		if job == nil || job.State != StateRunning {
			return false, ErrNotRunning
		}
		if job.CancelRequested || job.PurgeRequested {
			return false, context.Canceled
		}
		binding, err := readBinding(root, id)
		if err != nil {
			return false, err
		}
		binding.WaitingExecutor = waiting
		return false, writeAtomic(root, bindingName(id), binding)
	})
}

// ListDelegatedFor reads job state and waiting metadata in one short snapshot,
// avoiding a race with terminal cleanup removing the delegation sidecar.
func (s *Store) ListDelegatedFor(ctx context.Context, access Access) ([]Job, error) {
	out := []Job{}
	err := s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		for _, job := range j.Jobs {
			if job.Worker != WorkerDownloadV1 || !access.sees(job) {
				continue
			}
			if job.State == StateRunning {
				binding, err := readBinding(root, job.ID)
				if err != nil {
					return false, err
				}
				if binding.WaitingExecutor {
					job.State = "waiting_executor"
				}
			}
			out = append(out, job)
		}
		return false, nil
	})
	return out, err
}
