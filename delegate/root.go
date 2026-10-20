package delegate

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// Bounds of the executor's root programs (protocol: Root programs).
const (
	// MaxRootRequest is a root.run or root.start request line.
	MaxRootRequest = 1 << 20
	// MaxRootOutput is the largest output a policy may let a program write.
	MaxRootOutput = 64 << 20
	// RootRunTimeout is how long a client waits for root.run: the executor
	// answers within 130 seconds, and the client waits 10 longer.
	RootRunTimeout = 140 * time.Second
)

// Root job states.
const (
	RootJobRunning     = "running"
	RootJobStopping    = "stopping"
	RootJobSucceeded   = "succeeded"
	RootJobFailed      = "failed"
	RootJobCanceled    = "canceled"
	RootJobTimedOut    = "timed-out"
	RootJobInterrupted = "interrupted"
)

var (
	rootNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
	rootJobRE  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	rootFileRE = regexp.MustCompile(`^root-[a-f0-9]{32}$`)
)

// RootService is the executor's root work for an administrator's session:
// auth is that session's DSM pair, as for every executor call.
type RootService interface {
	Run(ctx context.Context, auth map[string]string, name string, params any) (RootResult, error)
	Start(ctx context.Context, auth map[string]string, name string, params any) (string, error)
	Jobs(ctx context.Context, auth map[string]string) ([]RootJob, error)
	Job(ctx context.Context, auth map[string]string, id string) (RootJob, error)
	Stop(ctx context.Context, auth map[string]string, id string) (RootJob, error)
}

// Root calls the executor's root ops. StagingDir is the package's
// var/executor-staging, where large root.run output arrives.
type Root struct {
	Client     ExecutorClient
	StagingDir string
}

var _ RootService = (*Root)(nil)

// LocalRoot is the package's executor (Local) and staging folder.
func LocalRoot(packageID string) *Root {
	return &Root{Client: Local(packageID), StagingDir: "/var/packages/" + packageID + "/var/executor-staging"}
}

// RootResult is a program that ran to its exit, whatever its code.
type RootResult struct {
	RequestID string
	ExitCode  int
	Stderr    string
	Output    []byte
}

// RootJob is a root job as root.jobs.get answers it.
type RootJob struct {
	ID           string          `json:"job_id"`
	Name         string          `json:"name"`
	State        string          `json:"state"`
	RequestedBy  string          `json:"requested_by"`
	StartedAt    time.Time       `json:"started_at"`
	EndedAt      *time.Time      `json:"ended_at,omitempty"`
	ExitCode     *int            `json:"exit_code,omitempty"`
	CancelReason string          `json:"cancel_reason,omitempty"`
	Progress     *RootProgress   `json:"progress,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	Output       string          `json:"output"`
}

type RootProgress struct {
	Percent *float64 `json:"percent,omitempty"`
	Message string   `json:"message,omitempty"`
	Phase   string   `json:"phase,omitempty"`
}

// Ended is a job that no longer runs.
func (j RootJob) Ended() bool { return j.State != RootJobRunning && j.State != RootJobStopping }

func rootRequest(name string, params any) (map[string]any, error) {
	if !rootNameRE.MatchString(name) {
		return nil, fail(CodeInvalidRequest)
	}
	p := map[string]any{"name": name}
	if params != nil {
		p["params"] = params
	}
	return p, nil
}

// Run runs a program and answers its exit code, stderr and output, reading
// and removing staged output.
func (r *Root) Run(ctx context.Context, auth map[string]string, name string, params any) (RootResult, error) {
	p, err := rootRequest(name, params)
	if err != nil {
		return RootResult{}, err
	}
	var out struct {
		RequestID string  `json:"request_id"`
		ExitCode  int     `json:"exit_code"`
		Stderr    string  `json:"stderr"`
		Output    *string `json:"output"`
		Staging   string  `json:"staging"`
		Size      int64   `json:"size"`
	}
	if err = r.Client.Call(ctx, "root.run", auth, p, &out); err != nil {
		return RootResult{}, err
	}
	res := RootResult{RequestID: out.RequestID, ExitCode: out.ExitCode, Stderr: out.Stderr}
	if out.Output != nil {
		res.Output = []byte(*out.Output)
		return res, nil
	}
	if res.Output, err = r.staged(out.Staging, out.Size); err != nil {
		return RootResult{}, err
	}
	return res, nil
}

// staged reads the root-<random> output file and removes it.
func (r *Root) staged(name string, size int64) ([]byte, error) {
	if !rootFileRE.MatchString(name) || size < 0 || size > MaxRootOutput || r.StagingDir == "" {
		return nil, fail(CodeExecutorProtocol)
	}
	root, err := os.OpenRoot(r.StagingDir)
	if err != nil {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	defer root.Close()
	defer root.Remove(name)
	f, err := root.Open(name)
	if err != nil {
		return nil, fail(CodeExecutorProtocol)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, size+1))
	if err != nil || int64(len(b)) != size {
		return nil, fail(CodeExecutorProtocol)
	}
	return b, nil
}

// Start starts a job and answers its job_id.
func (r *Root) Start(ctx context.Context, auth map[string]string, name string, params any) (string, error) {
	p, err := rootRequest(name, params)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"job_id"`
	}
	if err = r.Client.Call(ctx, "root.start", auth, p, &out); err != nil {
		return "", err
	}
	if !rootJobRE.MatchString(out.ID) {
		return "", fail(CodeExecutorProtocol)
	}
	return out.ID, nil
}

// Jobs is the package's jobs, newest first, as summaries: without Result,
// Output and the progress Message and Phase. Job answers the whole job.
func (r *Root) Jobs(ctx context.Context, auth map[string]string) ([]RootJob, error) {
	var out struct {
		Jobs []RootJob `json:"jobs"`
	}
	if err := r.Client.Call(ctx, "root.jobs.list", auth, nil, &out); err != nil {
		return nil, err
	}
	return out.Jobs, nil
}

func (r *Root) Job(ctx context.Context, auth map[string]string, id string) (RootJob, error) {
	return r.jobCall(ctx, "root.jobs.get", auth, id)
}

// Stop asks the job to cancel and answers it, stopping or already ended.
func (r *Root) Stop(ctx context.Context, auth map[string]string, id string) (RootJob, error) {
	return r.jobCall(ctx, "root.jobs.stop", auth, id)
}

func (r *Root) jobCall(ctx context.Context, op string, auth map[string]string, id string) (RootJob, error) {
	var job RootJob
	if !rootJobRE.MatchString(id) {
		return job, fail(CodeInvalidRequest)
	}
	if err := r.Client.Call(ctx, op, auth, map[string]string{"job_id": id}, &job); err != nil {
		return job, err
	}
	if job.ID != id || strings.TrimSpace(job.State) == "" {
		return job, fail(CodeExecutorProtocol)
	}
	return job, nil
}

// Wait polls the job every interval until it ends, handing each answer to
// progress when it is not nil.
func (r *Root) Wait(ctx context.Context, auth map[string]string, id string, interval time.Duration, progress func(RootJob)) (RootJob, error) {
	for {
		job, err := r.Job(ctx, auth, id)
		if err != nil {
			return job, err
		}
		if progress != nil {
			progress(job)
		}
		if job.Ended() {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return job, fail("canceled")
		case <-time.After(interval):
		}
	}
}
