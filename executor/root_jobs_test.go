package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// jobSetup is rootSetup whose fake programs read the job's control pipe
// (fd 4) and write its progress pipe (fd 5).
func jobSetup(t *testing.T, root string, program func(ctx context.Context, c rootCall, control *bufio.Reader, progress io.Writer) (int, error)) (*Executor, *fakeDSM, *fakeRoot) {
	t.Helper()
	e, f, r, _ := rootSetup(t, root)
	r.run = func(ctx context.Context, c rootCall) (int, error) {
		return program(ctx, c, bufio.NewReader(c.Files[0]), c.Files[1])
	}
	if err := e.startJobs(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.stopJobs("executor-stop") })
	return e, f, r
}

func startJob(t *testing.T, e *Executor, params map[string]any) string {
	t.Helper()
	p := map[string]any{"name": "clean"}
	if params != nil {
		p["params"] = params
	}
	out := result(t, request(e, "root.start", p))
	if out["state"] != "running" {
		t.Fatal(out)
	}
	return out["job_id"].(string)
}

// awaitJob is the job once its state is want.
func awaitJob(t *testing.T, e *Executor, id, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		job := result(t, request(e, "root.jobs.get", map[string]any{"job_id": id}))
		if job["state"] == want {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is %v, want %s: %v", id, job["state"], want, job)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var cleanJob = `{"clean":{"digest":"` + digestA + `","kind":"binary","schema":{"type":"object","additionalProperties":false,"properties":{"days":{"type":"integer","minimum":1}}},"job":{"grace_s":1%s}}}`

func cleanPolicy(extra string) string { return strings.Replace(cleanJob, "%s", extra, 1) }

func TestRootStartRunsAJobThatReportsProgressAndAResult(t *testing.T) {
	release := make(chan struct{})
	e, _, r := jobSetup(t, cleanPolicy(""), func(_ context.Context, c rootCall, _ *bufio.Reader, progress io.Writer) (int, error) {
		io.WriteString(progress, `{"percent":40,"message":"scanning","phase":"scan"}`+"\n")
		<-release
		io.WriteString(progress, `{"percent":100,"phase":"done","result":{"freed":3}}`+"\n")
		io.WriteString(c.Stdout, "log line\n")
		return 0, nil
	})
	id := startJob(t, e, map[string]any{"days": 7})
	if !idRE.MatchString(id) {
		t.Fatal(id)
	}
	running := awaitJob(t, e, id, "running")
	for running["progress"] == nil {
		running = awaitJob(t, e, id, "running")
	}
	if p := running["progress"].(map[string]any); p["percent"] != 40.0 || p["message"] != "scanning" || p["phase"] != "scan" {
		t.Fatal(running)
	}
	close(release)
	job := awaitJob(t, e, id, "succeeded")
	if job["name"] != "clean" || job["requested_by"] != "alice" || job["exit_code"] != 0.0 || job["output"] != "log line\n" || job["ended_at"] == nil {
		t.Fatal(job)
	}
	if res := job["result"].(map[string]any); res["freed"] != 3.0 {
		t.Fatal(job)
	}
	if _, ok := job["pid"]; ok {
		t.Fatal("the record's process is not part of the view", job)
	}
	c := r.started()[0]
	vars := env(c)
	if c.Mode != "job" || string(c.Stdin) != `{"days":7}` || vars["SYNOPKGLAND_ROOT_MODE"] != "job" || vars["SYNOPKGLAND_JOB_ID"] != id ||
		vars["SYNOPKGLAND_CONTROL_FD"] != "4" || vars["SYNOPKGLAND_PROGRESS_FD"] != "5" || vars["SYNOPKGLAND_REQUESTED_BY"] != "alice" || len(c.Files) != 2 {
		t.Fatalf("%+v %v", c, vars)
	}
	list := result(t, request(e, "root.jobs.list", map[string]any{}))
	if jobs := list["jobs"].([]any); len(jobs) != 1 || jobs[0].(map[string]any)["job_id"] != id {
		t.Fatal(list)
	}
}

func TestAFailedJobKeepsItsExitCode(t *testing.T) {
	e, _, _ := jobSetup(t, cleanPolicy(""), func(context.Context, rootCall, *bufio.Reader, io.Writer) (int, error) { return 4, nil })
	if job := awaitJob(t, e, startJob(t, e, nil), "failed"); job["exit_code"] != 4.0 {
		t.Fatal(job)
	}
}

func TestStoppingAJobAsksItToCancelThenKillsItAfterItsGrace(t *testing.T) {
	cooperative := true
	e, _, r := jobSetup(t, cleanPolicy(`,"max_concurrent":2`), func(ctx context.Context, _ rootCall, control *bufio.Reader, _ io.Writer) (int, error) {
		if !cooperative {
			<-ctx.Done()
			return -1, errKilled
		}
		line, _ := control.ReadString('\n')
		if line != `{"cancel":"user"}`+"\n" {
			return 9, nil
		}
		return 0, nil
	})
	id := startJob(t, e, nil)
	stopped := result(t, request(e, "root.jobs.stop", map[string]any{"job_id": id}))
	if stopped["state"] != "stopping" && stopped["state"] != "canceled" {
		t.Fatal(stopped)
	}
	if job := awaitJob(t, e, id, "canceled"); job["cancel_reason"] != "user" || job["exit_code"] != 0.0 {
		t.Fatal(job)
	}
	if r.killed() != 0 {
		t.Fatal("a job that ended on its own was killed")
	}
	// A stopped job stays stopped.
	if again := result(t, request(e, "root.jobs.stop", map[string]any{"job_id": id})); again["state"] != "canceled" {
		t.Fatal(again)
	}
	cooperative = false
	start := time.Now()
	id = startJob(t, e, nil)
	request(e, "root.jobs.stop", map[string]any{"job_id": id})
	job := awaitJob(t, e, id, "canceled")
	if elapsed := time.Since(start); elapsed < time.Second || elapsed > 5*time.Second || r.killed() != 1 || job["cancel_reason"] != "user" {
		t.Fatal(elapsed, r.killed(), job)
	}
}

func TestAJobPastItsTimeoutIsKilled(t *testing.T) {
	e, _, r := jobSetup(t, cleanPolicy(`,"timeout_s":1`), func(ctx context.Context, _ rootCall, _ *bufio.Reader, _ io.Writer) (int, error) {
		<-ctx.Done()
		return -1, errKilled
	})
	awaitJob(t, e, startJob(t, e, nil), "timed-out")
	if r.killed() != 1 {
		t.Fatal(r.killed())
	}
}

func TestRootJobsRunWithinTheirConcurrency(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	block := func(context.Context, rootCall, *bufio.Reader, io.Writer) (int, error) { <-release; return 0, nil }
	e, _, _ := jobSetup(t, cleanPolicy(""), block)
	startJob(t, e, nil)
	if out := request(e, "root.start", map[string]any{"name": "clean"}); out.Code != "ROOT_BUSY" {
		t.Fatal(out)
	}
	// The package runs at most four jobs, whatever its entries allow.
	entries := []string{}
	for i := range 5 {
		entries = append(entries, fmt.Sprintf(`"j%d":{"digest":"%s","job":{"max_concurrent":4}}`, i, digestA))
	}
	e, _, _ = jobSetup(t, "{"+strings.Join(entries, ",")+"}", block)
	for i := range 4 {
		result(t, request(e, "root.start", map[string]any{"name": fmt.Sprintf("j%d", i%2)}))
	}
	if out := request(e, "root.start", map[string]any{"name": "j4"}); out.Code != "ROOT_BUSY" {
		t.Fatal(out)
	}
}

func TestRootJobRefusals(t *testing.T) {
	e, f, r := jobSetup(t, `{"clean":{"digest":"`+digestA+`","job":{}},"probe":"`+digestA+`"}`, func(context.Context, rootCall, *bufio.Reader, io.Writer) (int, error) { return 0, nil })
	for _, tc := range []struct {
		op     string
		params map[string]any
		code   string
	}{
		{"root.start", map[string]any{"name": "probe"}, "POLICY_DENIED"},
		{"root.start", map[string]any{"name": "missing"}, "POLICY_DENIED"},
		{"root.start", map[string]any{"name": "clean", "params": map[string]any{"x": 1}}, "INVALID_REQUEST"},
		{"root.start", map[string]any{"name": "clean", "job_id": "mine"}, "INVALID_REQUEST"},
		{"root.run", map[string]any{"name": "clean"}, "POLICY_DENIED"},
		{"root.jobs.get", map[string]any{"job_id": strings.Repeat("b", 64)}, "ROOT_JOB_NOT_FOUND"},
		{"root.jobs.stop", map[string]any{"job_id": strings.Repeat("b", 64)}, "ROOT_JOB_NOT_FOUND"},
		{"root.jobs.get", map[string]any{"job_id": "../x"}, "INVALID_REQUEST"},
		{"root.jobs.get", map[string]any{}, "INVALID_REQUEST"},
	} {
		if out := request(e, tc.op, tc.params); out.Code != tc.code {
			t.Errorf("%s %v: %+v", tc.op, tc.params, out)
		}
	}
	f.user.Admin = false
	for _, op := range []string{"root.start", "root.jobs.list", "root.jobs.get", "root.jobs.stop"} {
		if out := request(e, op, map[string]any{"name": "clean", "job_id": strings.Repeat("b", 64)}); out.Code != "ADMIN_REQUIRED" && out.Code != "INVALID_REQUEST" {
			t.Error(op, out)
		}
	}
	if out := request(e, "root.jobs.list", map[string]any{}); out.Code != "ADMIN_REQUIRED" {
		t.Fatal(out)
	}
	f.user.Admin = true
	f.privilegeErr = fmt.Errorf("denied")
	if out := request(e, "root.jobs.list", map[string]any{}); out.Code != "APP_DENIED" {
		t.Fatal(out)
	}
	if len(r.started()) != 0 {
		t.Fatal("a refused start ran")
	}
}

func TestJobProgressTheExecutorIgnores(t *testing.T) {
	e, _, _ := jobSetup(t, cleanPolicy(""), func(_ context.Context, _ rootCall, _ *bufio.Reader, progress io.Writer) (int, error) {
		for _, line := range []string{
			`{"percent":10}`,
			`not json`,
			`{"percent":101}`,
			`{"percent":20,"extra":1}`,
			`{"message":"` + strings.Repeat("m", maxJobMessage+1) + `"}`,
			`{"result":"` + strings.Repeat("r", maxJobResult) + `"}`,
			strings.Repeat("x", maxJobProgressLine+10),
			`{"percent":30,"message":"ok"}`,
		} {
			io.WriteString(progress, line+"\n")
		}
		return 0, nil
	})
	job := awaitJob(t, e, startJob(t, e, nil), "succeeded")
	if p := job["progress"].(map[string]any); p["percent"] != 30.0 || p["message"] != "ok" || job["result"] != nil {
		t.Fatal(job)
	}
}

func TestStoppingTheExecutorCancelsItsJobs(t *testing.T) {
	e, _, r := jobSetup(t, cleanPolicy(`,"max_concurrent":2`), func(ctx context.Context, _ rootCall, control *bufio.Reader, _ io.Writer) (int, error) {
		line, _ := control.ReadString('\n')
		if line == `{"cancel":"stop"}`+"\n" {
			return 0, nil
		}
		<-ctx.Done()
		return -1, errKilled
	})
	a, b := startJob(t, e, nil), startJob(t, e, nil)
	e.stopJobs("stop")
	for _, id := range []string{a, b} {
		var job JobRecord
		if err := e.Store.record(jobRecordName(id), &job); err != nil || job.State != "canceled" || job.CancelReason != "stop" {
			t.Fatal(job, err)
		}
	}
	if r.killed() != 0 {
		t.Fatal(r.killed())
	}
	if out := request(e, "root.start", map[string]any{"name": "clean"}); out.Code != "EXECUTOR_PAUSED" {
		t.Fatal("a stopped job runtime started a job", out)
	}
}

func TestJobsLeftRunningByAnEndedExecutorAreInterrupted(t *testing.T) {
	e, _, _, _ := rootSetup(t, cleanPolicy(""))
	id := strings.Repeat("c", 64)
	stale := JobRecord{Version: 1, ID: id, Name: "clean", State: "running", RequestedBy: "alice", StartedAt: e.Now(), Pid: 1 << 22, PidStart: "1"}
	if err := e.Store.putJob(stale); err != nil {
		t.Fatal(err)
	}
	if err := e.startJobs(); err != nil {
		t.Fatal(err)
	}
	defer e.stopJobs("executor-stop")
	job := result(t, request(e, "root.jobs.get", map[string]any{"job_id": id}))
	if job["state"] != "interrupted" || job["ended_at"] == nil {
		t.Fatal(job)
	}
}

func TestOnlyTheNewestJobRecordsAreKept(t *testing.T) {
	e, _, _ := jobSetup(t, cleanPolicy(""), func(context.Context, rootCall, *bufio.Reader, io.Writer) (int, error) { return 0, nil })
	for i := range maxJobRecords {
		old := JobRecord{Version: 1, ID: fmt.Sprintf("%064x", i), Name: "clean", State: "succeeded", RequestedBy: "alice", StartedAt: time.Unix(int64(i), 0), EndedAt: time.Unix(int64(i), 0)}
		if err := e.Store.putJob(old); err != nil {
			t.Fatal(err)
		}
	}
	id := startJob(t, e, nil)
	awaitJob(t, e, id, "succeeded")
	jobs := result(t, request(e, "root.jobs.list", map[string]any{}))["jobs"].([]any)
	if len(jobs) != maxJobRecords || jobs[0].(map[string]any)["job_id"] != id {
		t.Fatal(len(jobs), jobs[0])
	}
	if out := request(e, "root.jobs.get", map[string]any{"job_id": fmt.Sprintf("%064x", 0)}); out.Code != "ROOT_JOB_NOT_FOUND" {
		t.Fatal("the oldest record remains", out)
	}
	b, _ := json.Marshal(jobs[len(jobs)-1])
	if !strings.Contains(string(b), fmt.Sprintf("%064x", 1)) {
		t.Fatal(string(b))
	}
}

// The list answers each job without its message, result and output, so the
// most records at their largest still fit a reply.
func TestTheJobListFitsAReplyWithTheLargestRecords(t *testing.T) {
	e, _, _ := jobSetup(t, cleanPolicy(""), func(context.Context, rootCall, *bufio.Reader, io.Writer) (int, error) { return 0, nil })
	percent := 40.0
	for i := range maxJobRecords {
		big := JobRecord{Version: 1, ID: fmt.Sprintf("%064x", i), Name: strings.Repeat("n", 128), State: "succeeded", RequestedBy: strings.Repeat("u", 64),
			StartedAt: time.Unix(int64(i), 0), EndedAt: time.Unix(int64(i), 0), CancelReason: "user",
			Progress: &JobProgress{Percent: &percent, Message: strings.Repeat("\x01", maxJobMessage), Phase: strings.Repeat("\x01", maxJobPhase)},
			Result:   json.RawMessage(`"` + strings.Repeat("r", maxJobResult-2) + `"`), Output: strings.Repeat("\x01", maxJobOutput)}
		if err := e.Store.putJob(big); err != nil {
			t.Fatal(err)
		}
	}
	out := request(e, "root.jobs.list", map[string]any{})
	b, _ := json.Marshal(out)
	if len(b) >= 64<<10 {
		t.Fatal("list reply", len(b))
	}
	job := out.Result.(map[string]any)["jobs"].([]map[string]any)[0]
	if _, ok := job["result"]; ok || job["output"] != nil || job["progress"].(map[string]any)["percent"] != 40.0 || len(job["progress"].(map[string]any)) != 1 {
		t.Fatal(job)
	}
	if full := result(t, request(e, "root.jobs.get", map[string]any{"job_id": fmt.Sprintf("%064x", 0)})); full["output"] == nil || full["result"] == nil {
		t.Fatal("get answers the whole record", full)
	}
}
