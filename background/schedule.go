package background

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"time"
)

const MaxSchedules = 8
const ManualQueueReserve = 4

// A tick delayed past this window is missed, never a catch-up submission.
const ScheduleWindow = 60 * time.Second

var ErrScheduleFull = errors.New("background schedule limit reached")
var ErrInputUnavailable = errors.New("the imported private copy is unavailable")

type Schedule struct {
	ID              string    `json:"id"`
	Key             string    `json:"key"`
	Owner           Submitter `json:"owner"`
	SourceJobID     string    `json:"sourceJobId"`
	Name            string    `json:"name"`
	InputDigest     string    `json:"inputDigest"`
	InputBytes      int       `json:"inputBytes"`
	WorkerRevision  int       `json:"workerRevision"`
	IntervalSeconds int       `json:"intervalSeconds"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"createdAt"`
	NextAt          time.Time `json:"nextAt"`
	LastTriggeredAt time.Time `json:"lastTriggeredAt,omitzero"`
	Missed          uint64    `json:"missed"`
	LastMissedAt    time.Time `json:"lastMissedAt,omitzero"`
	Reason          string    `json:"reason,omitempty"`
}
type ScheduleRequest struct {
	Key             string `json:"key"`
	JobID           string `json:"jobId"`
	IntervalSeconds int    `json:"intervalSeconds"`
	Consent         bool   `json:"consent"`
}

func validInterval(seconds int) bool { return seconds == 3600 || seconds == 86400 || seconds == 604800 }
func (a Access) seesSchedule(sc Schedule) bool {
	return a.Admin || (a.Owner.Valid() && sc.Owner == a.Owner)
}
func findSchedule(j *journal, id string) *Schedule {
	if !jobID.MatchString(id) {
		return nil
	}
	for i := range j.Schedules {
		if j.Schedules[i].ID == id {
			return &j.Schedules[i]
		}
	}
	return nil
}
func (s *Store) Schedules(ctx context.Context, a Access) ([]Schedule, error) {
	out := []Schedule{}
	err := s.withJournal(ctx, func(_ *os.Root, j *journal) (bool, error) {
		for _, sc := range j.Schedules {
			if a.seesSchedule(sc) {
				out = append(out, sc)
			}
		}
		return false, nil
	})
	return out, err
}
func readCopy(root *os.Root, id, digest string, size int) ([]byte, error) {
	if !jobID.MatchString(id) || size < 1 || size > MaxInputBytes {
		return nil, ErrInputUnavailable
	}
	f, err := root.OpenFile(inputs+"/"+id, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrInputUnavailable
	}
	defer f.Close()
	if checkPrivate(f) != nil {
		return nil, ErrInputUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxInputBytes+1))
	if err != nil || len(b) != size || sha256hex(b) != digest {
		return nil, ErrInputUnavailable
	}
	return b, nil
}
func (s *Store) CreateSchedule(ctx context.Context, r ScheduleRequest, a Access) (Schedule, bool, error) {
	var out Schedule
	dup := false
	if !a.Owner.Valid() || !r.Consent || !validInterval(r.IntervalSeconds) || !keyRE.MatchString(r.Key) {
		return out, false, ErrInvalid
	}
	err := s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		for _, sc := range j.Schedules {
			if sc.Owner == a.Owner && sc.Key == r.Key {
				if sc.SourceJobID != r.JobID || sc.IntervalSeconds != r.IntervalSeconds {
					return false, ErrKeyConflict
				}
				out, dup = sc, true
				return false, nil
			}
		}
		job := find(j, r.JobID)
		if job == nil || job.PurgeRequested || job.Submitter != a.Owner {
			return false, ErrNotFound
		}
		// Scheduling only a completed import avoids racing a mutable/incomplete source.
		if job.State != StateSucceeded || job.ScheduleID != "" || job.WorkerRevision != s.revision {
			return false, ErrInputUnavailable
		}
		data, err := readCopy(root, job.ID, job.InputDigest, job.InputBytes)
		if err != nil {
			return false, err
		}
		if len(j.Schedules) >= MaxSchedules {
			return false, ErrScheduleFull
		}
		_, ownerBytes := j.ownerUsage(a.Owner)
		if ownerBytes+int64(len(data)) > MaxOwnerBytes {
			return false, ErrOwnerQuota
		}
		_, used := j.pending()
		if used+int64(len(data)) > s.limits.MaxTotalBytes {
			return false, ErrQuota
		}
		id, err := newID()
		if err != nil {
			return false, err
		}
		if err := writeInput(root, id, data); err != nil {
			return false, err
		}
		now := s.now().UTC()
		out = Schedule{ID: id, Key: r.Key, Owner: a.Owner, SourceJobID: job.ID, Name: job.Name, InputDigest: job.InputDigest, InputBytes: job.InputBytes, WorkerRevision: s.revision, IntervalSeconds: r.IntervalSeconds, Enabled: true, CreatedAt: now, NextAt: now.Add(time.Duration(r.IntervalSeconds) * time.Second)}
		j.Schedules = append(j.Schedules, out)
		return true, nil
	})
	return out, dup, err
}

// SetSchedule(false) journals revocation before the cancel watcher can observe it.
// Enable is administrator-only and requires fresh explicit consent at the route.
func (s *Store) SetSchedule(ctx context.Context, id string, enabled bool, a Access) (Schedule, error) {
	var out Schedule
	err := s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		sc := findSchedule(j, id)
		if sc == nil || !a.seesSchedule(*sc) {
			return false, ErrNotFound
		}
		if enabled {
			if !a.Admin {
				return false, ErrInvalid
			}
			if _, err := readCopy(root, sc.ID, sc.InputDigest, sc.InputBytes); err != nil {
				return false, err
			}
			sc.WorkerRevision = s.revision
			sc.Enabled = true
			sc.Reason = ""
			sc.NextAt = s.now().UTC().Add(time.Duration(sc.IntervalSeconds) * time.Second)
		} else {
			sc.Enabled = false
			sc.Reason = "disabled"
			s.cancelScheduleJobs(root, j, id)
		}
		out = *sc
		return true, nil
	})
	return out, err
}
func (s *Store) cancelScheduleJobs(root *os.Root, j *journal, id string) {
	for i := range j.Jobs {
		job := &j.Jobs[i]
		if job.ScheduleID != id {
			continue
		}
		if job.State == StateQueued || job.State == StatePaused {
			s.finishJob(root, job, StateCanceled, "schedule-disabled")
		} else if job.State == StateRunning {
			job.CancelRequested = true
		}
	}
}
func (s *Store) DeleteSchedule(ctx context.Context, id string, a Access) error {
	return s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		sc := findSchedule(j, id)
		if sc == nil || !a.seesSchedule(*sc) {
			return false, ErrNotFound
		}
		s.cancelScheduleJobs(root, j, id)
		kept := j.Schedules[:0]
		for _, v := range j.Schedules {
			if v.ID != id {
				kept = append(kept, v)
			}
		}
		j.Schedules = kept
		return true, nil
	})
}

type tickCopy struct {
	schedule Schedule
	err      error
}

func (s *Store) reconcileSchedules(root *os.Root, j *journal, recovering bool, copies map[string]tickCopy) bool {
	changed := false
	now := s.now().UTC()
	for i := range j.Schedules {
		sc := &j.Schedules[i]
		if !validInterval(sc.IntervalSeconds) || sc.NextAt.IsZero() {
			sc.Enabled = false
			sc.Reason = "invalid-schedule"
			s.cancelScheduleJobs(root, j, sc.ID)
			changed = true
			continue
		}
		if sc.WorkerRevision != s.revision {
			sc.Enabled = false
			sc.Reason = "worker-changed"
			s.cancelScheduleJobs(root, j, sc.ID)
			changed = true
			continue
		}
		if !sc.Enabled || sc.NextAt.After(now) {
			continue
		}
		due := sc.NextAt
		period := time.Duration(sc.IntervalSeconds) * time.Second
		count := uint64(now.Sub(due)/period) + 1
		// Preparation happens without the journal lock. A concurrent disable,
		// deletion, reapproval or tick invalidates that snapshot. Do not advance
		// its checkpoint unless this admission still uses the exact schedule.
		copy, prepared := copies[sc.ID]
		if !recovering && count == 1 && now.Sub(due) <= ScheduleWindow && (!prepared || copy.schedule != *sc) {
			continue
		}
		sc.NextAt = due.Add(time.Duration(count) * period)
		changed = true
		// Recovery deliberately skips even a just-due tick. There is no startup burst.
		if recovering || count > 1 || now.Sub(due) > ScheduleWindow {
			sc.Missed += count
			sc.LastMissedAt = due
			sc.Reason = "missed"
			continue
		}
		if copy.err != nil {
			sc.Enabled = false
			sc.Reason = "input-unavailable"
			sc.Missed++
			sc.LastMissedAt = due
			continue
		}
		pending, ownerBytes := j.ownerUsage(sc.Owner)
		countPending, used := j.pending()
		reason := ""
		switch {
		case pending >= MaxOwnerPending:
			reason = "owner-queue-full"
		case ownerBytes+int64(sc.InputBytes+resultReserve) > MaxOwnerBytes:
			reason = "owner-quota-exceeded"
		case countPending >= MaxQueued-ManualQueueReserve:
			reason = "queue-full"
		case used+int64(sc.InputBytes+resultReserve) > s.limits.MaxTotalBytes:
			reason = "quota-exceeded"
		}
		if reason != "" {
			sc.Missed++
			sc.LastMissedAt = due
			sc.Reason = reason
			continue
		}
		id, err := newID()
		if err != nil {
			sc.Missed++
			sc.LastMissedAt = due
			sc.Reason = "unavailable"
			continue
		}
		// Private inputs are immutable. Link the verified schedule copy instead
		// of reading, hashing and copying its contents under the journal lock.
		// The worker checks the digest again when it consumes the linked input.
		if err := root.Link(inputs+"/"+sc.ID, inputs+"/"+id); err != nil {
			sc.Missed++
			sc.LastMissedAt = due
			sc.Reason = "unavailable"
			continue
		}
		if err := syncDir(root, inputs); err != nil {
			_ = root.Remove(inputs + "/" + id)
			sc.Missed++
			sc.LastMissedAt = due
			sc.Reason = "unavailable"
			continue
		}
		sc.LastTriggeredAt = due
		sc.Reason = ""
		j.Jobs = append(j.Jobs, Job{ID: id, Key: "schedule-" + id, Name: sc.Name, State: StateQueued, Submitter: sc.Owner, Worker: WorkerSHA256V1, WorkerRevision: s.revision, InputDigest: sc.InputDigest, InputBytes: sc.InputBytes, SubmittedAt: now, ScheduleID: sc.ID, ScheduledAt: due})
	}
	j.trim()
	return changed
}

// Tick advances each durable checkpoint and creates a job in the same commit.
// Multiple processes therefore cannot admit the same schedule trigger twice.
func (s *Store) Tick(ctx context.Context) error {
	schedules, err := s.Schedules(ctx, Access{Admin: true})
	if err != nil {
		return err
	}
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	copies := make(map[string]tickCopy)
	now := s.now().UTC()
	for _, sc := range schedules {
		if !sc.Enabled || sc.WorkerRevision != s.revision || !validInterval(sc.IntervalSeconds) || sc.NextAt.IsZero() || sc.NextAt.After(now) || now.Sub(sc.NextAt) > ScheduleWindow {
			continue
		}
		_, err := s.readScheduleCopy(ctx, root, sc.ID, sc.InputDigest, sc.InputBytes)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		copies[sc.ID] = tickCopy{schedule: sc, err: err}
	}
	return s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		return s.reconcileSchedules(root, j, false, copies), nil
	})
}
