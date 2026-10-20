package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Bounds of root jobs (protocol: Root jobs).
const (
	rootJobSlots       = 4
	maxJobRecords      = 64
	maxJobProgressLine = 32 << 10
	maxJobMessage      = 512
	maxJobPhase        = 64
	maxJobResult       = 16 << 10
	maxJobOutput       = 4 << 10
	// A package stop, upgrade or uninstall waits at most this for its jobs.
	stopJobGrace = 15 * time.Second
	// Running progress reaches the record at most this often.
	jobPersistEvery = time.Second
)

// JobRecord is a root job, kept in the protected store as
// rootjob-<id>.record. Pid and PidStart name its process group while it runs.
type JobRecord struct {
	Version      int             `json:"v"`
	ID           string          `json:"job_id"`
	Name         string          `json:"name"`
	State        string          `json:"state"`
	RequestedBy  string          `json:"requested_by"`
	StartedAt    time.Time       `json:"started_at"`
	EndedAt      time.Time       `json:"ended_at,omitzero"`
	ExitCode     *int            `json:"exit_code,omitempty"`
	CancelReason string          `json:"cancel_reason,omitempty"`
	Progress     *JobProgress    `json:"progress,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	Output       string          `json:"output"`
	Pid          int             `json:"pid,omitempty"`
	PidStart     string          `json:"pid_start,omitempty"`
}

type JobProgress struct {
	Percent *float64 `json:"percent,omitempty"`
	Message string   `json:"message,omitempty"`
	Phase   string   `json:"phase,omitempty"`
}

func validJobState(state string) bool {
	return contains([]string{"running", "stopping", "succeeded", "failed", "canceled", "timed-out", "interrupted"}, state)
}

func (r JobRecord) ended() bool { return r.State != "running" && r.State != "stopping" }

// view is the job as clients see it.
func (r JobRecord) view() map[string]any {
	b, _ := json.Marshal(r)
	var m map[string]any
	json.Unmarshal(b, &m)
	delete(m, "v")
	delete(m, "pid")
	delete(m, "pid_start")
	return m
}

// summary is the view a list answers: without the message, result and
// output, so maxJobRecords of them fit a reply.
func (r JobRecord) summary() map[string]any {
	m := r.view()
	delete(m, "result")
	delete(m, "output")
	delete(m, "progress")
	if r.Progress != nil && r.Progress.Percent != nil {
		m["progress"] = map[string]any{"percent": *r.Progress.Percent}
	}
	return m
}

func jobRecordName(id string) string { return "rootjob-" + id + ".record" }

func (s *Store) putJob(r JobRecord) error {
	if r.Version != 1 || !idRE.MatchString(r.ID) || !tokenRE.MatchString(r.Name) || !validJobState(r.State) {
		return errors.New("root job")
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > maxRequest {
		return errors.New("root job size")
	}
	return s.atomic(jobRecordName(r.ID), b)
}

func (s *Store) job(id string) (JobRecord, error) {
	var r JobRecord
	if err := s.record(jobRecordName(id), &r); err != nil {
		return r, err
	}
	if r.Version != 1 || r.ID != id || !validJobState(r.State) {
		return r, errors.New("root job schema")
	}
	return r, nil
}

// jobs is every readable job record, newest first; one that cannot be read
// is logged and left out.
func (s *Store) jobs() ([]JobRecord, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	out := []JobRecord{}
	for _, ent := range entries {
		id, ok := strings.CutPrefix(ent.Name(), "rootjob-")
		if id, ok = strings.CutSuffix(id, ".record"); !ok || !idRE.MatchString(id) {
			continue
		}
		r, err := s.job(id)
		if err != nil {
			log.Printf("root job record unreadable: job=%s", id)
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// pruneJobs removes the oldest ended records beyond maxJobRecords.
func (s *Store) pruneJobs() error {
	all, err := s.jobs()
	if err != nil {
		return err
	}
	for i := len(all) - 1; i >= maxJobRecords; i-- {
		if all[i].ended() {
			os.Remove(s.Dir + "/" + jobRecordName(all[i].ID))
		}
	}
	return nil
}

// rootJobs are the jobs of one open service; stopped refuses new ones.
type rootJobs struct {
	mu      sync.Mutex
	stopped bool
	running map[string]*rootJob
	wg      sync.WaitGroup
}

type rootJob struct {
	rec    JobRecord // guarded by rootJobs.mu
	cancel chan string
}

// startJobs ends the records a previous executor left running, killing
// their process group when the same process still runs, and admits jobs.
func (e *Executor) startJobs() error {
	recs, err := e.Store.jobs()
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.ended() {
			continue
		}
		if r.Pid > 0 {
			if start, err := processStart(r.Pid); err == nil && start == r.PidStart {
				killGroup(r.Pid)
			}
		}
		r.State, r.EndedAt, r.Pid, r.PidStart = "interrupted", e.Now().UTC(), 0, ""
		if err = e.Store.putJob(r); err != nil {
			return err
		}
	}
	e.rootMu.Lock()
	e.jobs = &rootJobs{running: map[string]*rootJob{}}
	e.rootMu.Unlock()
	return nil
}

// stopJobs cancels every running job with reason and waits until all ended.
func (e *Executor) stopJobs(reason string) {
	e.rootMu.Lock()
	j := e.jobs
	e.rootMu.Unlock()
	if j == nil {
		return
	}
	j.mu.Lock()
	j.stopped = true
	for _, job := range j.running {
		job.stopLocked(reason)
	}
	j.mu.Unlock()
	j.wg.Wait()
}

// setJobEnd records why the service will close next: stop, upgrade or
// purge; anything else is the executor stopping.
func (e *Executor) setJobEnd(reason string) {
	e.rootMu.Lock()
	defer e.rootMu.Unlock()
	e.jobEndReason = reason
}

func (e *Executor) jobEnd() string {
	e.rootMu.Lock()
	defer e.rootMu.Unlock()
	if e.jobEndReason == "" {
		return "executor-stop"
	}
	return e.jobEndReason
}

func (e *Executor) rootJobs() *rootJobs {
	e.rootMu.Lock()
	defer e.rootMu.Unlock()
	return e.jobs
}

// stopLocked asks the job to cancel once.
func (job *rootJob) stopLocked(reason string) {
	if job.rec.State != "running" {
		return
	}
	job.rec.State, job.rec.CancelReason = "stopping", reason
	job.cancel <- reason
}

func (e *Executor) rootStart(ctx context.Context, auth Auth, p Params) Response {
	entry, code := e.rootEntry(p.Name)
	if code == "" && entry.Job == nil {
		code = "POLICY_DENIED"
	}
	if code != "" {
		return deny(code)
	}
	stdin, ok := rootParams(entry, p.RootParams)
	if !ok {
		return deny("INVALID_REQUEST")
	}
	s, code := e.rootCaller(ctx, auth)
	if code != "" {
		return deny(code)
	}
	j := e.rootJobs()
	if j == nil {
		return deny("EXECUTOR_PAUSED")
	}
	if e.Runner == nil || (e.Policy.RootState && e.RootState == "") {
		return deny("ROOT_FAILED")
	}
	id, err := randomID()
	if err != nil {
		return deny("INTERNAL")
	}
	job := &rootJob{rec: JobRecord{Version: 1, ID: id, Name: p.Name, State: "running", RequestedBy: s.Username, StartedAt: e.Now().UTC()}, cancel: make(chan string, 1)}
	if code = j.admit(job, entry.Job.concurrency()); code != "" {
		return deny(code)
	}
	control, controlEnd, err := os.Pipe()
	if err != nil {
		j.release(job)
		return deny("ROOT_FAILED")
	}
	progressEnd, progress, err := os.Pipe()
	if err != nil {
		control.Close()
		controlEnd.Close()
		j.release(job)
		return deny("ROOT_FAILED")
	}
	output := &syncHead{head: headBuffer{limit: maxJobOutput}}
	call := rootCall{Name: p.Name, Entry: entry, Mode: "job", Stdin: stdin, Stdout: output, Stderr: output, Files: []*os.File{control, progress},
		Env: e.rootEnv("job", "SYNOPKGLAND_REQUESTED_BY="+s.Username, "SYNOPKGLAND_JOB_ID="+id, "SYNOPKGLAND_CONTROL_FD=4", "SYNOPKGLAND_PROGRESS_FD=5")}
	proc, err := e.Runner.Start(call)
	control.Close()
	progress.Close()
	if err != nil {
		controlEnd.Close()
		progressEnd.Close()
		j.release(job)
		e.auditRoot(s, "root.start", id, "failed")
		return deny("ROOT_FAILED")
	}
	j.mu.Lock()
	if pid := proc.Group(); pid > 0 {
		job.rec.Pid = pid
		job.rec.PidStart, _ = processStart(pid)
	}
	rec := job.rec
	j.mu.Unlock()
	if err = e.Store.putJob(rec); err == nil {
		err = e.Store.pruneJobs()
	}
	if err != nil {
		log.Printf("root job record: job=%s: %v", id, err)
	}
	e.auditRoot(s, "root.start", id, "started")
	go e.superviseJob(j, job, proc, controlEnd, progressEnd, output, entry.Job)
	return success(map[string]any{"job_id": id, "state": "running"})
}

// admit reserves a slot for job within the entry's and the package's limits.
func (j *rootJobs) admit(job *rootJob, concurrency int) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.stopped {
		return "EXECUTOR_PAUSED"
	}
	same := 0
	for _, other := range j.running {
		if other.rec.Name == job.rec.Name {
			same++
		}
	}
	if len(j.running) >= rootJobSlots || same >= concurrency {
		return "ROOT_BUSY"
	}
	j.running[job.rec.ID] = job
	j.wg.Add(1)
	return ""
}

func (j *rootJobs) release(job *rootJob) {
	j.mu.Lock()
	delete(j.running, job.rec.ID)
	j.mu.Unlock()
	j.wg.Done()
}

// superviseJob waits for the job, sends its cancel, kills its process group
// after the grace or at the timeout, and records how it ended.
func (e *Executor) superviseJob(j *rootJobs, job *rootJob, proc rootProcess, control, progress *os.File, output *syncHead, limits *RootJob) {
	defer j.release(job)
	defer control.Close()
	type exit struct {
		code int
		err  error
	}
	exits := make(chan exit, 1)
	go func() { code, err := proc.Wait(); exits <- exit{code, err} }()
	reading := make(chan struct{})
	go func() { defer close(reading); e.readProgress(j, job, progress) }()
	timeout := time.NewTimer(limits.timeout())
	defer timeout.Stop()
	var kill <-chan time.Time
	timedOut := false
	var x exit
wait:
	for {
		select {
		case x = <-exits:
			break wait
		case <-timeout.C:
			timedOut = true
			proc.Kill()
		case reason := <-job.cancel:
			grace := limits.grace()
			if reason != "user" {
				limit := e.Policy.budgets().Duration("stop") / 3
				if e.Policy.Timeouts == nil {
					limit = stopJobGrace
				}
				e.rootMu.Lock()
				until := e.shutdownUntil
				e.rootMu.Unlock()
				if !until.IsZero() {
					limit = min(limit, max(0, time.Until(until)/3))
				}
				grace = min(grace, limit)
			}
			j.mu.Lock()
			rec := job.rec
			j.mu.Unlock()
			e.Store.putJob(rec)
			line, _ := json.Marshal(map[string]string{"cancel": reason})
			control.Write(append(line, '\n'))
			kill = time.After(grace)
		case <-kill:
			kill = nil
			proc.Kill()
		}
	}
	// A child the job left behind may hold the progress pipe open.
	select {
	case <-reading:
	case <-time.After(time.Second):
	}
	progress.Close()
	<-reading
	j.mu.Lock()
	r := &job.rec
	switch {
	case timedOut:
		r.State = "timed-out"
	case r.CancelReason != "":
		r.State = "canceled"
	case x.err == nil && x.code == 0:
		r.State = "succeeded"
	default:
		r.State = "failed"
	}
	if x.err == nil {
		code := x.code
		r.ExitCode = &code
	}
	r.EndedAt, r.Output, r.Pid, r.PidStart = e.Now().UTC(), strings.ToValidUTF8(output.String(), "�"), 0, ""
	rec := *r
	j.mu.Unlock()
	if err := e.Store.putJob(rec); err != nil {
		log.Printf("root job record: job=%s: %v", rec.ID, err)
	}
	e.auditRoot(Subject{Username: rec.RequestedBy}, "root.job", rec.ID, rec.State)
}

// jobUpdate is one progress line; every member is optional.
type jobUpdate struct {
	Percent *float64        `json:"percent"`
	Message *string         `json:"message"`
	Phase   *string         `json:"phase"`
	Result  json.RawMessage `json:"result"`
}

func (u jobUpdate) valid() bool {
	return (u.Percent == nil || (*u.Percent >= 0 && *u.Percent <= 100)) &&
		(u.Message == nil || utf8.RuneCountInString(*u.Message) <= maxJobMessage) &&
		(u.Phase == nil || utf8.RuneCountInString(*u.Phase) <= maxJobPhase) &&
		len(u.Result) <= maxJobResult
}

// readProgress applies the job's progress lines until EOF; a line that is
// too long, not a valid update or has unknown members is ignored.
func (e *Executor) readProgress(j *rootJobs, job *rootJob, f io.Reader) {
	r := bufio.NewReaderSize(f, maxJobProgressLine)
	var persisted time.Time
	for {
		line, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			for err == bufio.ErrBufferFull {
				_, err = r.ReadSlice('\n')
			}
			if err != nil {
				return
			}
			continue
		}
		var u jobUpdate
		if len(line) > 0 && strictJSON(line, &u) == nil && u.valid() {
			j.mu.Lock()
			p := JobProgress{}
			if job.rec.Progress != nil {
				p = *job.rec.Progress
			}
			if u.Percent != nil {
				p.Percent = u.Percent
			}
			if u.Message != nil {
				p.Message = *u.Message
			}
			if u.Phase != nil {
				p.Phase = *u.Phase
			}
			job.rec.Progress = &p
			if len(u.Result) > 0 {
				job.rec.Result = append(json.RawMessage(nil), u.Result...)
			}
			rec := job.rec
			j.mu.Unlock()
			if time.Since(persisted) >= jobPersistEvery {
				persisted = time.Now()
				e.Store.putJob(rec)
			}
		}
		if err != nil {
			return
		}
	}
}

// job is the running job's latest record, or the stored one.
func (e *Executor) job(id string) (JobRecord, *rootJob, string) {
	if j := e.rootJobs(); j != nil {
		j.mu.Lock()
		job := j.running[id]
		if job != nil {
			rec := job.rec
			j.mu.Unlock()
			return rec, job, ""
		}
		j.mu.Unlock()
	}
	rec, err := e.Store.job(id)
	if os.IsNotExist(err) {
		return rec, nil, "ROOT_JOB_NOT_FOUND"
	}
	if err != nil {
		return rec, nil, "STORAGE_ERROR"
	}
	return rec, nil, ""
}

func (e *Executor) rootJobOp(ctx context.Context, op string, auth Auth, p Params) Response {
	if op != "root.jobs.list" && !idRE.MatchString(p.Job) {
		return deny("INVALID_REQUEST")
	}
	s, code := e.rootCaller(ctx, auth)
	if code != "" {
		return deny(code)
	}
	switch op {
	case "root.jobs.list":
		recs, err := e.Store.jobs()
		if err != nil {
			return deny("STORAGE_ERROR")
		}
		jobs := []map[string]any{}
		for _, r := range recs {
			if live, _, code := e.job(r.ID); code == "" {
				r = live
			}
			jobs = append(jobs, r.summary())
		}
		return success(map[string]any{"jobs": jobs})
	case "root.jobs.stop":
		rec, job, code := e.job(p.Job)
		if code != "" {
			return deny(code)
		}
		if job != nil {
			j := e.rootJobs()
			j.mu.Lock()
			job.stopLocked("user")
			rec = job.rec
			j.mu.Unlock()
			e.auditRoot(s, "root.jobs.stop", p.Job, "cancel-requested")
		}
		return success(rec.view())
	}
	rec, _, code := e.job(p.Job)
	if code != "" {
		return deny(code)
	}
	return success(rec.view())
}

func (e *Executor) auditRoot(s Subject, operation, job, result string) {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	if err := e.auditLocked(s, operation, job, "", result); err != nil {
		log.Printf("root audit: job=%s: %v", job, err)
	}
}

// syncHead is a headBuffer a job writes while its record is read.
type syncHead struct {
	mu   sync.Mutex
	head headBuffer
}

func (b *syncHead) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.head.Write(p)
}

func (b *syncHead) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.head.buf.String()
}

// parseProcessStart is field 22 of /proc/<pid>/stat, the process start time.
func parseProcessStart(stat string) (string, error) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return "", errors.New("process stat")
	}
	fields := strings.Fields(stat[i+1:])
	if len(fields) < 20 {
		return "", errors.New("process stat")
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", err
	}
	return fields[19], nil
}
