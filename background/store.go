// Package background is the package-private spool, journal and fixed worker
// behind opt-in background jobs. The CGI submits and reads jobs; a package
// user unit runs the worker. Both run as the package account and share one
// 0700 directory, so no root helper, task scheduler or DSM session is needed.
package background

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// Product limits, not DSM limits.
const (
	WorkerSHA256V1 = "sha256-v1"
	// WorkerRevision identifies the worker code. A journal written under another
	// revision never has its queued jobs run by this one.
	WorkerRevision  = 1
	MaxInputBytes   = 512 * 1024
	MaxQueued       = 16
	MaxOwnerPending = 2
	MaxOwnerBytes   = 1 << 20
	MaxPurgeAudit   = 64
	MaxTotalBytes   = 16 << 20
	// JobTimeout is independent of HTTP. Apps can set Runner.Timeout for their
	// fixed Work; cancel, pause and package stop still interrupt it.
	JobTimeout = 30 * time.Minute
	// RequestLockTimeout bounds journal contention, not job execution. Leave
	// time for authentication, file import and the response. Workers have no
	// lock cap.
	RequestLockTimeout = 5 * time.Second
	// MaxHistory bounds finished jobs kept for reading results and for key reuse.
	MaxHistory = 64

	// Schema 3 adds JSON results and singleton scopes. Read schemas 1 and 2;
	// older writers refuse schema 3 rather than silently drop these fields.
	schemaVersion = 3
	// Pending jobs reserve the maximum result; terminal jobs charge actual
	// encoded size, with the existing minimum receipt allowance.
	resultReserve = 512 + MaxResultBytes
	maxJournal    = 4 << 20
	// MaxCheckpointBytes bounds the resume state a Work saves with Task.Checkpoint.
	MaxCheckpointBytes = 16 << 10
	// MaxProgressItemBytes bounds Progress.Item; a longer item is cut.
	MaxProgressItemBytes = 1024
)

type State string

const (
	StateQueued      State = "queued"
	StateRunning     State = "running"
	StateSucceeded   State = "succeeded"
	StateFailed      State = "failed"
	StateCanceled    State = "canceled"
	StateInterrupted State = "interrupted"
	// StatePaused holds a job with its input and checkpoint until it is resumed.
	StatePaused State = "paused"
)

// Why a job is paused: a user's pause stays until a resume; a package stop
// is resumed when the worker starts again.
const (
	PauseUser    = "user"
	PauseStopped = "stopped"
)

func (s State) terminal() bool { return s != StateQueued && s != StateRunning && s != StatePaused }

var (
	ErrBusy              = errors.New("background journal is busy; retry later")
	ErrOwnerQueueFull    = errors.New("owner pending quota reached")
	ErrOwnerQuota        = errors.New("owner storage quota reached")
	ErrInvalid           = errors.New("invalid background request")
	ErrTooLarge          = errors.New("input is larger than the per-file limit")
	ErrQueueFull         = errors.New("background queue is full")
	ErrQuota             = errors.New("background spool quota reached")
	ErrKeyConflict       = errors.New("idempotency key already used for another input")
	ErrNotFound          = errors.New("job not found")
	ErrNotCancelable     = errors.New("job already finished")
	ErrNotRunning        = errors.New("job is not running")
	ErrNotPausable       = errors.New("job cannot be paused or resumed")
	ErrUnsafeSpool       = errors.New("background spool is not private to the package account")
	ErrJournalUnreadable = errors.New("background journal unreadable")
	ErrJournalSchema     = errors.New("background journal has an unsupported schema")
)

// Submitter binds visibility to a fresh DSM username and UID. It never grants
// filesystem permissions or changes the worker identity.
type Submitter struct {
	User string `json:"user"`
	UID  string `json:"uid"`
}

// Progress is what a Work reports through Task.Report. The Store stamps UpdatedAt.
type Progress struct {
	Phase      string    `json:"phase,omitempty"`
	Done       int64     `json:"done"`
	Total      int64     `json:"total"`
	Bytes      int64     `json:"bytes"`
	TotalBytes int64     `json:"totalBytes"`
	Item       string    `json:"item,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Result struct {
	// JSON is an optional bounded structured result. The hash receipt remains
	// available for legacy workers; it is not a digest of JSON.
	JSON   json.RawMessage `json:"json,omitempty"`
	SHA256 string          `json:"sha256"`
	Bytes  int             `json:"bytes"`
}

type Job struct {
	Singleton        string    `json:"singleton,omitempty"`
	AdmissionPending bool      `json:"admission_pending,omitempty"`
	DelegationID     string    `json:"delegation_id,omitempty"`
	CopyRetained     bool      `json:"copyRetained,omitempty"`
	ScheduleID       string    `json:"scheduleId,omitempty"`
	ScheduledAt      time.Time `json:"scheduledAt,omitzero"`
	ID               string    `json:"id"`
	Key              string    `json:"key"`
	Name             string    `json:"name"`
	State            State     `json:"state"`
	PurgeRequested   bool      `json:"purgeRequested,omitempty"`
	CancelRequested  bool      `json:"cancelRequested,omitempty"`
	PauseRequested   bool      `json:"pauseRequested,omitempty"`
	PauseReason      string    `json:"pauseReason,omitempty"`
	PausedAt         time.Time `json:"pausedAt,omitzero"`
	Submitter        Submitter `json:"submitter"`
	Worker           string    `json:"worker"`
	WorkerRevision   int       `json:"workerRevision"`
	InputDigest      string    `json:"inputDigest"`
	InputBytes       int       `json:"inputBytes"`
	SubmittedAt      time.Time `json:"submittedAt"`
	StartedAt        time.Time `json:"startedAt,omitzero"`
	FinishedAt       time.Time `json:"finishedAt,omitzero"`
	ErrorCode        string    `json:"errorCode,omitempty"`
	Result           *Result   `json:"result,omitempty"`
	Progress         *Progress `json:"progress,omitempty"`
	// Checkpoint is the resume state the Work last saved; cleared when the job ends.
	Checkpoint string `json:"checkpoint,omitempty"`
}

type SubmitRequest struct {
	Key       string
	Name      string
	Data      []byte
	Submitter Submitter
}

// Outcome is what a worker reports for the job it claimed. StatePaused keeps
// the job, its input and Checkpoint for a later run.
type Outcome struct {
	State       State
	ErrorCode   string
	Result      *Result
	Progress    *Progress
	Checkpoint  string
	PauseReason string
}

type WorkerInfo struct {
	PID      int       `json:"pid"`
	UID      int       `json:"uid"`
	Username string    `json:"username"`
	Revision int       `json:"revision"`
	At       time.Time `json:"at"`
}

type Status struct {
	Algorithm string      `json:"algorithm"`
	Revision  int         `json:"revision"`
	Queued    int         `json:"queued"`
	Running   int         `json:"running"`
	Paused    int         `json:"paused"`
	Worker    *WorkerInfo `json:"worker,omitempty"`
}

type Limits struct{ MaxTotalBytes int64 }

type journal struct {
	Schedules  []Schedule   `json:"schedules,omitempty"`
	PurgeAudit []PurgeAudit `json:"purgeAudit,omitempty"`
	Schema     int          `json:"schema"`
	Revision   int          `json:"workerRevision"`
	Jobs       []Job        `json:"jobs"`
}

// Store is safe for concurrent use by several processes: journal operations run
// under an exclusive flock and commit with write, fsync, rename. Schedule input
// verification runs outside that lock and rechecks its snapshot before commit.
type Store struct {
	dir              string
	limits           Limits
	revision         int
	now              func() time.Time
	lockTimeout      time.Duration
	readScheduleCopy func(context.Context, *os.Root, string, string, int) ([]byte, error)
}

func NewStore(dir string) *Store {
	return &Store{dir: dir, limits: Limits{MaxTotalBytes: MaxTotalBytes}, revision: WorkerRevision, now: time.Now,
		readScheduleCopy: func(ctx context.Context, root *os.Root, id, digest string, size int) ([]byte, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			data, err := readCopy(root, id, digest, size)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return data, err
		}}
}

// NewRequestStore bounds every CGI journal acquisition, including reads and
// cancellation. NewStore remains the worker's context-controlled wait policy.
func NewRequestStore(dir string) *Store {
	s := NewStore(dir)
	s.lockTimeout = RequestLockTimeout
	return s
}

var (
	jobID  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	keyRE  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	inputs = "inputs"
)

func validName(name string) bool {
	if n := utf8.RuneCountInString(name); n < 1 || n > 128 || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

// withJournal runs fn on the journal under the spool lock. fn reports whether
// it changed anything; only then is the journal rewritten.
func (s *Store) withJournal(ctx context.Context, fn func(root *os.Root, j *journal) (bool, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	// Exclusive creation avoids first-use OpenFile(O_CREATE) races on Darwin.
	// The lock is never removed; other processes open the existing inode.
	lock, err := root.OpenFile("lock", os.O_CREATE|os.O_EXCL|os.O_RDWR|unix.O_NOFOLLOW, 0o600)
	if errors.Is(err, os.ErrExist) {
		lock, err = root.OpenFile("lock", os.O_RDWR|unix.O_NOFOLLOW, 0o600)
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := checkPrivate(lock); err != nil {
		return err
	}
	lockCtx := ctx
	if s.lockTimeout > 0 {
		var cancel context.CancelFunc
		lockCtx, cancel = context.WithTimeout(ctx, s.lockTimeout)
		defer cancel()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if lockCtx.Err() != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			return ErrBusy
		}
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return err
		}
		select {
		case <-lockCtx.Done():
			if err := ctx.Err(); err != nil {
				return err
			}
			return ErrBusy
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if err := ctx.Err(); err != nil {
		return err
	}
	j, err := s.readJournal(root)
	if err != nil {
		return err
	}
	changed, err := fn(root, &j)
	if err != nil || !changed {
		return err
	}
	if err := writeAtomic(root, "journal.json", &j); err != nil {
		return err
	}
	s.removeOrphanInputs(root, &j)
	return nil
}

// openRoot creates the spool on first use and refuses one that is a symlink,
// belongs to someone else or is readable by other accounts.
func (s *Store) openRoot() (*os.Root, error) {
	if err := os.Mkdir(s.dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(s.dir)
	if err != nil {
		return nil, err
	}
	if err := checkPrivateInfo(info, true); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, err
	}
	if err := root.Mkdir(inputs, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		root.Close()
		return nil, err
	}
	if info, err := root.Lstat(inputs); err != nil || checkPrivateInfo(info, true) != nil {
		root.Close()
		return nil, ErrUnsafeSpool
	}
	return root, nil
}

func checkPrivate(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return checkPrivateInfo(info, false)
}

func checkPrivateInfo(info os.FileInfo, dir bool) error {
	if dir != info.IsDir() || (!dir && !info.Mode().IsRegular()) || info.Mode().Perm()&0o077 != 0 || !ownedByUs(info) {
		return ErrUnsafeSpool
	}
	return nil
}

func (s *Store) readJournal(root *os.Root) (journal, error) {
	j := journal{Schema: schemaVersion, Revision: s.revision}
	f, err := root.OpenFile("journal.json", os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return j, err
	}
	defer f.Close()
	if err := checkPrivate(f); err != nil {
		return j, err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxJournal+1))
	if err != nil || len(b) > maxJournal {
		return j, ErrJournalUnreadable
	}
	if err := json.Unmarshal(b, &j); err != nil || (j.Schema < 1 || j.Schema > schemaVersion) {
		return journal{}, ErrJournalSchema
	}
	j.Schema = schemaVersion
	return j, nil
}

func writeAtomic(root *os.Root, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	tmp := "." + name + "-" + hex.EncodeToString(nonce)
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return syncDir(root, ".")
}

func syncDir(root *os.Root, name string) error {
	d, err := root.Open(name)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func find(j *journal, id string) *Job {
	if !jobID.MatchString(id) {
		return nil
	}
	for i := range j.Jobs {
		if j.Jobs[i].ID == id {
			return &j.Jobs[i]
		}
	}
	return nil
}

func (j *journal) pending() (count int, bytes int64) {
	for _, job := range j.Jobs {
		if !job.State.terminal() {
			count++
			bytes += int64(job.InputBytes + resultReserve)
		} else {
			if job.CopyRetained {
				bytes += int64(job.InputBytes)
			}
			if job.Result != nil {
				bytes += resultCharge(job.Result)
			}
		}
	}
	for _, sc := range j.Schedules {
		bytes += int64(sc.InputBytes)
	}
	return count, bytes
}

// trim keeps pending jobs and the newest MaxHistory finished ones.
func (j *journal) trim() {
	finished := 0
	for _, job := range j.Jobs {
		if job.State.terminal() {
			finished++
		}
	}
	kept := j.Jobs[:0]
	for _, job := range j.Jobs {
		if job.State.terminal() && finished > MaxHistory {
			finished--
			continue
		}
		kept = append(kept, job)
	}
	j.Jobs = kept
}

func (s *Store) removeInput(root *os.Root, id string) {
	_ = root.Remove(inputs + "/" + id)
}

func (s *Store) finishJob(root *os.Root, job *Job, state State, code string) {
	job.State, job.ErrorCode, job.FinishedAt = state, code, s.now().UTC()
	job.PauseRequested, job.PauseReason, job.PausedAt, job.Checkpoint = false, "", time.Time{}, ""
	if state != StateSucceeded {
		job.CopyRetained = false
	}
	if !job.CopyRetained {
		s.removeInput(root, job.ID)
	}
}

func (s *Store) Submit(ctx context.Context, req SubmitRequest) (Job, bool, error) {
	return s.submit(ctx, req, "")
}

func (s *Store) submit(ctx context.Context, req SubmitRequest, singleton string) (Job, bool, error) {
	if len(req.Data) > MaxInputBytes {
		return Job{}, false, ErrTooLarge
	}
	if !keyRE.MatchString(req.Key) || !validName(req.Name) || len(req.Data) == 0 || !req.Submitter.Valid() {
		return Job{}, false, ErrInvalid
	}
	digest := sha256hex(req.Data)
	var created Job
	var duplicate bool
	err := s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		for _, job := range j.Jobs {
			if job.Submitter == req.Submitter && job.Key == req.Key && !job.PurgeRequested {
				if job.InputDigest != digest || job.Singleton != singleton {
					return false, ErrKeyConflict
				}
				created, duplicate = job, true
				return false, nil
			}
		}
		if singleton != "" {
			for _, job := range j.Jobs {
				if job.Singleton == singleton && job.Submitter == req.Submitter && !job.State.terminal() && !job.PurgeRequested {
					created, duplicate = job, true
					return false, nil
				}
			}
		}
		// Reserve the input and one bounded result for each pending job. Finished
		// result charges remain until history trimming or an explicit admin purge.
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
		id, err := newID()
		if err != nil {
			return false, err
		}
		if err := writeInput(root, id, req.Data); err != nil {
			return false, err
		}
		created = Job{
			ID: id, Key: req.Key, Name: req.Name, Singleton: singleton, CopyRetained: true, State: StateQueued, Submitter: req.Submitter,
			Worker: WorkerSHA256V1, WorkerRevision: s.revision, InputDigest: digest,
			InputBytes: len(req.Data), SubmittedAt: s.now().UTC(),
		}
		j.Jobs = append(j.Jobs, created)
		j.trim()
		// A journal commit failure surfaces below; the input is then orphaned
		// and removed by the next Recover.
		return true, nil
	})
	return created, duplicate, err
}

func writeInput(root *os.Root, id string, data []byte) error {
	f, err := root.OpenFile(inputs+"/"+id, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = syncDir(root, inputs)
	}
	if err != nil {
		_ = root.Remove(inputs + "/" + id)
	}
	return err
}

func (s *Store) List(ctx context.Context) ([]Job, error) { return s.ListFor(ctx, Access{Admin: true}) }
func (s *Store) ListFor(ctx context.Context, access Access) ([]Job, error) {
	var jobs []Job
	err := s.withJournal(ctx, func(_ *os.Root, j *journal) (bool, error) {
		for _, job := range j.Jobs {
			if access.sees(job) {
				jobs = append(jobs, job)
			}
		}
		return false, nil
	})
	return jobs, err
}

// Get is the internal worker view, including a pending purge so cancellation drains.
func (s *Store) Get(ctx context.Context, id string) (Job, error) { return s.get(ctx, id, nil) }
func (s *Store) GetFor(ctx context.Context, id string, access Access) (Job, error) {
	return s.get(ctx, id, &access)
}
func (s *Store) get(ctx context.Context, id string, access *Access) (Job, error) {
	var out Job
	err := s.withJournal(ctx, func(_ *os.Root, j *journal) (bool, error) {
		job := find(j, id)
		if job == nil || (access != nil && !access.sees(*job)) {
			return false, ErrNotFound
		}
		out = *job
		return false, nil
	})
	return out, err
}

func (s *Store) Cancel(ctx context.Context, id string) (Job, error) {
	return s.CancelFor(ctx, id, Access{Admin: true})
}
func (s *Store) CancelFor(ctx context.Context, id string, access Access) (Job, error) {
	var out Job
	err := s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		job := find(j, id)
		if job == nil || !access.sees(*job) {
			return false, ErrNotFound
		}
		switch job.State {
		case StateQueued, StatePaused:
			s.finishJob(root, job, StateCanceled, "")
		case StateRunning:
			job.CancelRequested = true
		default:
			return false, ErrNotCancelable
		}
		out = *job
		return true, nil
	})
	return out, err
}

// Claim hands the oldest queued job to the worker, or nil when there is none,
// or another job is still running.
func (s *Store) Claim(ctx context.Context) (*Job, error) {
	var out *Job
	err := s.withJournal(ctx, func(_ *os.Root, j *journal) (bool, error) {
		var next *Job
		for i := range j.Jobs {
			switch job := &j.Jobs[i]; job.State {
			case StateRunning:
				return false, nil
			case StateQueued:
				if next == nil && !job.AdmissionPending {
					next = job
				}
			}
		}
		if next == nil {
			return false, nil
		}
		next.State, next.StartedAt = StateRunning, s.now().UTC()
		claimed := *next
		out = &claimed
		return true, nil
	})
	return out, err
}

// Finish records the outcome of a running job. A cancel request that was
// journaled first wins over a success, failure or pause that arrives later.
func (s *Store) Finish(ctx context.Context, id string, outcome Outcome) error {
	if !outcome.State.terminal() && outcome.State != StatePaused {
		return ErrInvalid
	}
	if outcome.State == StatePaused && !validCheckpoint(outcome.Checkpoint) {
		return ErrInvalid
	}
	return s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		job := find(j, id)
		if job == nil || job.State != StateRunning {
			return false, ErrNotRunning
		}
		if outcome.Progress != nil {
			job.Progress = s.stamp(*outcome.Progress)
		}
		if job.CancelRequested {
			outcome = Outcome{State: StateCanceled}
		}
		if outcome.State == StateSucceeded && outcome.Result != nil {
			if code := validateResult(outcome.Result); code != "" {
				outcome = Outcome{State: StateFailed, ErrorCode: code}
			}
		}
		if outcome.State == StatePaused {
			s.pause(job, outcome)
			return true, nil
		}
		s.finishJob(root, job, outcome.State, outcome.ErrorCode)
		if outcome.State == StateSucceeded {
			job.Result = outcome.Result
		}
		j.removePurged()
		j.trim()
		return true, nil
	})
}

// Recover runs when the worker starts. A job still marked running lost its
// worker: it is interrupted (or canceled when a cancel was already requested)
// and never replayed. Queued jobs journaled for another worker revision are
// interrupted too, so an upgrade cannot silently run old work with new code.
func (s *Store) Recover(ctx context.Context) error {
	return s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		changed := j.Revision != s.revision
		for i := range j.Jobs {
			job := &j.Jobs[i]
			switch {
			case job.AdmissionPending:
				job.AdmissionPending = false
				if !job.State.terminal() {
					s.finishJob(root, job, StateInterrupted, "admission-interrupted")
				}
				changed = true
			case job.State == StateRunning && job.CancelRequested:
				s.finishJob(root, job, StateCanceled, "")
				changed = true
			case job.State == StateRunning:
				s.finishJob(root, job, StateInterrupted, "worker-stopped")
				changed = true
			case (job.State == StateQueued || job.State == StatePaused) && j.Revision != s.revision:
				s.finishJob(root, job, StateInterrupted, "worker-changed")
			case job.State == StatePaused && job.PauseReason == PauseStopped:
				resume(job)
				changed = true
			}
		}
		j.removePurged()
		changed = s.reconcileSchedules(root, j, true, nil) || changed
		j.Revision = s.revision
		s.removeOrphanInputs(root, j)
		return changed, nil
	})
}

func (s *Store) removeOrphanInputs(root *os.Root, j *journal) {
	dir, err := root.Open(inputs)
	if err != nil {
		return
	}
	names, _ := dir.Readdirnames(-1)
	dir.Close()
	live := map[string]bool{}
	for _, job := range j.Jobs {
		if !job.State.terminal() || job.CopyRetained {
			live[job.ID] = true
		}
	}
	for _, sc := range j.Schedules {
		live[sc.ID] = true
	}
	for _, name := range names {
		if !live[name] {
			_ = root.Remove(inputs + "/" + name)
		}
	}
}

// OpenInput returns the stored input of the job that is currently running.
func (s *Store) OpenInput(id string) (*os.File, error) {
	var f *os.File
	err := s.withJournal(context.Background(), func(root *os.Root, j *journal) (bool, error) {
		job := find(j, id)
		if job == nil || job.State != StateRunning {
			return false, ErrNotRunning
		}
		var err error
		f, err = root.OpenFile(inputs+"/"+id, os.O_RDONLY|unix.O_NOFOLLOW, 0)
		return false, err
	})
	return f, err
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	return s.StatusFor(ctx, Access{Admin: true})
}
func (s *Store) StatusFor(ctx context.Context, access Access) (Status, error) {
	st := Status{Algorithm: WorkerSHA256V1, Revision: s.revision}
	err := s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		for _, job := range j.Jobs {
			if !access.sees(job) {
				continue
			}
			switch job.State {
			case StateQueued:
				st.Queued++
			case StateRunning:
				st.Running++
			case StatePaused:
				st.Paused++
			}
		}
		st.Worker = s.readWorker()
		return false, nil
	})
	return st, err
}

func ownedByUs(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (j *journal) removePurged() {
	kept := j.Jobs[:0]
	for _, job := range j.Jobs {
		if !job.PurgeRequested || !job.State.terminal() {
			kept = append(kept, job)
		}
	}
	j.Jobs = kept
}

// PurgeOwner preserves other owners and records an audit in the same durable
// journal. Running jobs become hidden tombstones until Finish/Recover drains them.
func (s *Store) PurgeOwner(ctx context.Context, owner Submitter, actor string) (PurgeAudit, error) {
	if owner.User == "" || actor == "" {
		return PurgeAudit{}, ErrInvalid
	}
	audit := PurgeAudit{Owner: owner, Actor: actor, At: s.now().UTC()}
	err := s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		for i := range j.Jobs {
			job := &j.Jobs[i]
			if job.Submitter != owner || job.PurgeRequested {
				continue
			}
			job.PurgeRequested = true
			if job.State == StateRunning {
				job.CancelRequested = true
				audit.Draining++
			} else {
				s.finishJob(root, job, StateCanceled, "purged")
				audit.Removed++
			}
		}
		kept := j.Schedules[:0]
		for _, sc := range j.Schedules {
			if sc.Owner != owner {
				kept = append(kept, sc)
			}
		}
		j.Schedules = kept
		j.removePurged()
		j.PurgeAudit = append(j.PurgeAudit, audit)
		if len(j.PurgeAudit) > MaxPurgeAudit {
			j.PurgeAudit = j.PurgeAudit[len(j.PurgeAudit)-MaxPurgeAudit:]
		}
		return true, nil
	})
	return audit, err
}
