package background

import (
	"context"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// PauseFor pauses a queued job at once and asks the worker to pause a running
// one at its next checkpoint. Pausing a paused job keeps it paused until a
// resume, even one a package stop paused. Delegated jobs keep their own
// executor recovery and cannot be paused.
func (s *Store) PauseFor(ctx context.Context, id string, access Access) (Job, error) {
	return s.pausable(ctx, id, access, func(job *Job) {
		switch job.State {
		case StateQueued:
			job.State, job.PausedAt = StatePaused, s.now().UTC()
		case StateRunning:
			job.PauseRequested = true
		}
		job.PauseReason = PauseUser
	})
}

// ResumeFor queues a paused job again with its checkpoint, or withdraws the
// pause request of a running one.
func (s *Store) ResumeFor(ctx context.Context, id string, access Access) (Job, error) {
	return s.pausable(ctx, id, access, func(job *Job) {
		switch job.State {
		case StatePaused:
			resume(job)
		case StateRunning:
			job.PauseRequested, job.PauseReason = false, ""
		}
	})
}

func (s *Store) pausable(ctx context.Context, id string, access Access, change func(*Job)) (Job, error) {
	var out Job
	err := s.withJournal(ctx, func(_ *os.Root, j *journal) (bool, error) {
		job := find(j, id)
		if job == nil || !access.sees(*job) {
			return false, ErrNotFound
		}
		if job.State.terminal() || job.DelegationID != "" || job.AdmissionPending {
			return false, ErrNotPausable
		}
		change(job)
		out = *job
		return true, nil
	})
	return out, err
}

func resume(job *Job) {
	job.State, job.PauseReason, job.PausedAt = StateQueued, "", time.Time{}
}

// pause parks a running job; a user's pause request outlasts a package stop.
func (s *Store) pause(job *Job, outcome Outcome) {
	if !job.PauseRequested {
		job.PauseReason = outcome.PauseReason
	}
	job.State, job.PauseRequested, job.PausedAt, job.Checkpoint = StatePaused, false, s.now().UTC(), outcome.Checkpoint
}

// Report records the running job's progress (when not nil) and checkpoint,
// and returns the job so the worker sees cancel and pause requests.
func (s *Store) Report(ctx context.Context, id string, progress *Progress, checkpoint string) (Job, error) {
	if !validCheckpoint(checkpoint) {
		return Job{}, ErrInvalid
	}
	var out Job
	err := s.withJournal(ctx, func(_ *os.Root, j *journal) (bool, error) {
		job := find(j, id)
		if job == nil || job.State != StateRunning {
			return false, ErrNotRunning
		}
		if progress != nil {
			job.Progress = s.stamp(*progress)
		}
		job.Checkpoint = checkpoint
		out = *job
		return true, nil
	})
	return out, err
}

func validCheckpoint(c string) bool { return len(c) <= MaxCheckpointBytes && utf8.ValidString(c) }

func (s *Store) stamp(p Progress) *Progress {
	p.Phase = boundedText(p.Phase, MaxProgressPhaseBytes)
	p.Item = boundedText(p.Item, MaxProgressItemBytes)
	p.UpdatedAt = s.now().UTC()
	return &p
}

func boundedText(text string, limit int) string {
	text = strings.ToValidUTF8(text, "")
	if len(text) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	return text
}
