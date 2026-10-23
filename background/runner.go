package background

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"sync"
	"time"
)

// Work is one fixed worker algorithm. It receives only the job's input bytes.
// It returns when ctx ends: a cancel, a pause, a package stop or the time
// limit. A paused or stopped job runs again later from its last Checkpoint
// (Task.Job.Checkpoint), or from the start when it saved none, so a Work must
// be safe to repeat from any checkpoint it saved.
type Work func(ctx context.Context, t *Task) (Result, error)

// Task is the job a Work runs: a snapshot of the claimed job and its input.
// The Runner publishes what Report and Checkpoint record while the job runs.
type Task struct {
	Job   Job
	Input io.Reader

	mu         sync.Mutex
	progress   *Progress
	checkpoint string
	changed    bool
}

// Report records the job's progress; the latest one is published.
func (t *Task) Report(p Progress) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.progress, t.changed = &p, true
}

// Checkpoint records the state to resume from, valid UTF-8 of at most
// MaxCheckpointBytes. Save one only once the work it describes is done.
func (t *Task) Checkpoint(state string) error {
	if !validCheckpoint(state) {
		return ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checkpoint, t.changed = state, true
	return nil
}

// update returns what changed since the last call.
func (t *Task) update() (progress *Progress, checkpoint string, changed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	changed, t.changed = t.changed, false
	return t.progress, t.checkpoint, changed
}

// SHA256V1 hashes the imported private copy; delegate assembly adds a separate fixed worker.
func SHA256V1(ctx context.Context, t *Task) (Result, error) { return HashInput(ctx, t.Input) }

// HashInput is the sha256-v1 algorithm over any reader.
func HashInput(ctx context.Context, in io.Reader) (Result, error) {
	h := sha256.New()
	buf := make([]byte, 32*1024)
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		n, err := in.Read(buf)
		h.Write(buf[:n])
		total += n
		if errors.Is(err, io.EOF) {
			return Result{SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: total}, nil
		}
		if err != nil {
			return Result{}, err
		}
	}
}

var errRefusedRoot = errors.New("the background worker refuses to run as root")

// RefuseRoot keeps the worker on the package account.
func RefuseRoot(uid int) error {
	if uid == 0 {
		return errRefusedRoot
	}
	return nil
}

var (
	errCancelRequested = errors.New("cancel requested")
	errPauseRequested  = errors.New("pause requested")
	errTimeout         = errors.New("job time limit reached")
)

// Runner claims jobs from the Store and runs them one at a time.
type Runner struct {
	Store      *Store
	Work       Work
	Timeout    time.Duration
	Poll       time.Duration
	CancelPoll time.Duration
	// ProgressEvery spaces journal writes of reported progress; zero writes
	// at every CancelPoll that saw a change.
	ProgressEvery time.Duration
	Beat          time.Duration
	Info          WorkerInfo
}

// Run returns nil after ctx ends and the job in flight, if any, is recorded
// as paused by a stop; the next Run resumes it. Any other return is a spool failure.
func (r *Runner) Run(ctx context.Context) error {
	if err := r.Store.Recover(ctx); err != nil {
		return err
	}
	if err := r.Store.Heartbeat(ctx, r.Info); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	var beats sync.WaitGroup
	defer func() { cancel(); beats.Wait(); r.Store.ClearHeartbeat(r.Info) }()
	beats.Add(1)
	go func() { defer beats.Done(); r.heartbeat(ctx) }()
	for ctx.Err() == nil {
		if err := r.Store.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Printf("schedule tick failed: %v", err)
		}
		job, err := r.Store.Claim(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("claim failed: %v", err)
		}
		if job == nil || err != nil {
			sleep(ctx, r.Poll)
			continue
		}
		r.Process(ctx, job)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (r *Runner) heartbeat(ctx context.Context) {
	for {
		sleep(ctx, max(r.Beat, 10*time.Millisecond))
		if ctx.Err() != nil {
			return
		}
		if err := r.Store.Heartbeat(ctx, r.Info); err != nil && ctx.Err() == nil {
			log.Printf("heartbeat failed: %v", err)
		}
	}
}

// Process runs one claimed job and records its outcome: a parent that ends
// pauses it as stopped. Run calls it; a worker with its own loop may too.
func (r *Runner) Process(parent context.Context, job *Job) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	timer := time.AfterFunc(r.Timeout, func() { cancel(errTimeout) })
	defer timer.Stop()
	task := &Task{Job: *job, checkpoint: job.Checkpoint}
	var watcher sync.WaitGroup
	watcher.Add(1)
	go func() { defer watcher.Done(); r.watch(ctx, task, cancel) }()

	result, err := r.run(ctx, task)
	outcome := Outcome{State: StateSucceeded, Result: &result}
	switch {
	case parent.Err() != nil:
		outcome = Outcome{State: StatePaused, PauseReason: PauseStopped}
	case errors.Is(context.Cause(ctx), errCancelRequested):
		outcome = Outcome{State: StateCanceled}
	case errors.Is(context.Cause(ctx), errPauseRequested):
		outcome = Outcome{State: StatePaused, PauseReason: PauseUser}
	case errors.Is(context.Cause(ctx), errTimeout):
		outcome = Outcome{State: StateFailed, ErrorCode: "timeout"}
	case errors.Is(err, errInputChanged):
		outcome = Outcome{State: StateFailed, ErrorCode: "input-changed"}
	case err != nil:
		log.Printf("job %s failed: %v", job.ID, err)
		outcome = Outcome{State: StateFailed, ErrorCode: "worker-error"}
	}
	cancel(nil)
	watcher.Wait()
	outcome.Progress, outcome.Checkpoint, _ = task.update()
	// The stop signal that ended parent must not prevent recording the outcome.
	fin, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := r.Store.Finish(fin, job.ID, outcome); err != nil {
		log.Printf("job %s outcome not recorded: %v", job.ID, err)
	}
}

var errInputChanged = errors.New("stored input no longer matches its digest")

func (r *Runner) run(ctx context.Context, task *Task) (Result, error) {
	job := &task.Job
	f, err := r.Store.OpenInput(job.ID)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	// Verify the imported input independently of the Work's result. A JSON
	// worker need not return an input hash, and must not run on changed input.
	data, err := io.ReadAll(io.LimitReader(f, MaxInputBytes+1))
	if err != nil {
		return Result{}, err
	}
	receipt, err := HashInput(ctx, bytes.NewReader(data))
	if err != nil {
		return Result{}, err
	}
	if receipt.SHA256 != job.InputDigest || receipt.Bytes != job.InputBytes {
		return Result{}, errInputChanged
	}
	// Work consumes exactly the verified snapshot, even if another package
	// process replaces the imported file while this job is running.
	task.Input = bytes.NewReader(data)
	result, err := r.Work(ctx, task)
	if err != nil {
		return Result{}, err
	}
	if len(result.JSON) == 0 && (result.SHA256 != job.InputDigest || result.Bytes != job.InputBytes) {
		return Result{}, errInputChanged
	}
	return result, nil
}

// watch publishes the task's progress and ends the job context on a cancel
// or pause request.
func (r *Runner) watch(ctx context.Context, task *Task, cancel context.CancelCauseFunc) {
	var published time.Time
	for {
		sleep(ctx, r.CancelPoll)
		if ctx.Err() != nil {
			return
		}
		job, err := r.poll(ctx, task, &published)
		switch {
		case err != nil:
		case job.CancelRequested || job.State != StateRunning:
			cancel(errCancelRequested)
			return
		case job.PauseRequested:
			cancel(errPauseRequested)
			return
		}
	}
}

// poll reads the job, writing the task's changes first when ProgressEvery allows.
func (r *Runner) poll(ctx context.Context, task *Task, published *time.Time) (Job, error) {
	if time.Since(*published) >= r.ProgressEvery {
		if progress, checkpoint, changed := task.update(); changed {
			*published = time.Now()
			return r.Store.Report(ctx, task.Job.ID, progress, checkpoint)
		}
	}
	return r.Store.Get(ctx, task.Job.ID)
}

// NewRunner is the production assembly: the fixed sha256-v1 worker over the
// package-private spool at dir.
func NewRunner(dir string, info WorkerInfo) *Runner {
	return &Runner{
		Store: NewStore(dir), Work: SHA256V1, Timeout: JobTimeout,
		Poll: 500 * time.Millisecond, CancelPoll: 250 * time.Millisecond, ProgressEvery: time.Second, Beat: 5 * time.Second, Info: info,
	}
}
