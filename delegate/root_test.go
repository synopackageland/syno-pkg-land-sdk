package delegate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var adminAuth = map[string]string{"dsm_cookie": "id", "dsm_synotoken": "token", "remote_addr": "192.0.2.1"}

type rootCall struct {
	op     string
	auth   map[string]string
	params map[string]any
}

// rootExecutor answers each op with its JSON result, or a refusal code.
func rootExecutor(replies map[string]string) (fakeExecutor, *[]rootCall) {
	calls := &[]rootCall{}
	return func(_ context.Context, op string, auth map[string]string, in, out any) error {
		b, _ := json.Marshal(in)
		var params map[string]any
		json.Unmarshal(b, &params)
		*calls = append(*calls, rootCall{op, auth, params})
		reply := replies[op]
		if !strings.HasPrefix(reply, "{") {
			return fail(reply)
		}
		return json.Unmarshal([]byte(reply), out)
	}, calls
}

func TestRootRunSendsParamsAndAnswersTheProgramsOutput(t *testing.T) {
	ex, calls := rootExecutor(map[string]string{"root.run": `{"request_id":"r","exit_code":3,"stderr":"warn","output":"{\"ok\":true}"}`})
	r := &Root{Client: ex, StagingDir: t.TempDir()}
	out, err := r.Run(context.Background(), adminAuth, "cli", map[string]any{"method": "volumes"})
	if err != nil || out.ExitCode != 3 || out.Stderr != "warn" || string(out.Output) != `{"ok":true}` || out.RequestID != "r" {
		t.Fatal(out, err)
	}
	if c := (*calls)[0]; c.op != "root.run" || !reflect.DeepEqual(c.auth, adminAuth) || c.params["name"] != "cli" || c.params["params"].(map[string]any)["method"] != "volumes" {
		t.Fatal(c)
	}
	// Without params the program gets {}: nothing is sent.
	r.Run(context.Background(), adminAuth, "cli", nil)
	if _, ok := (*calls)[1].params["params"]; ok {
		t.Fatal((*calls)[1])
	}
}

func TestRootRunReadsAndRemovesStagedOutput(t *testing.T) {
	dir := t.TempDir()
	name := "root-" + strings.Repeat("a", 32)
	os.WriteFile(filepath.Join(dir, name), []byte("big output"), 0600)
	ex, _ := rootExecutor(map[string]string{"root.run": `{"request_id":"r","exit_code":0,"stderr":"","staging":"` + name + `","size":10}`})
	r := &Root{Client: ex, StagingDir: dir}
	out, err := r.Run(context.Background(), adminAuth, "cli", nil)
	if err != nil || string(out.Output) != "big output" {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatal("staged output remains")
	}
	for _, reply := range []string{
		`{"request_id":"r","exit_code":0,"stderr":"","staging":"read-x","size":10}`,
		`{"request_id":"r","exit_code":0,"stderr":"","staging":"../root-x","size":10}`,
		`{"request_id":"r","exit_code":0,"stderr":"","staging":"` + name + `","size":10}`,
		`{"request_id":"r","exit_code":0,"stderr":"","staging":"` + name + `","size":67108865}`,
	} {
		os.WriteFile(filepath.Join(dir, name), []byte("short"), 0600)
		ex, _ := rootExecutor(map[string]string{"root.run": reply})
		_, err := (&Root{Client: ex, StagingDir: dir}).Run(context.Background(), adminAuth, "cli", nil)
		code(t, err, CodeExecutorProtocol)
	}
}

func TestRootRunWithoutItsStagingFolderIsStagingUnavailable(t *testing.T) {
	name := "root-" + strings.Repeat("a", 32)
	ex, _ := rootExecutor(map[string]string{"root.run": `{"request_id":"r","exit_code":0,"stderr":"","staging":"` + name + `","size":10}`})
	r := &Root{Client: ex, StagingDir: filepath.Join(t.TempDir(), "executor-staging")}
	_, err := r.Run(context.Background(), adminAuth, "cli", nil)
	code(t, err, "STAGING_UNAVAILABLE")
}

func TestRootRefusalsKeepTheirCode(t *testing.T) {
	ex, _ := rootExecutor(map[string]string{"root.run": CodeRootTimeout, "root.start": CodeRootBusy, "root.jobs.get": CodeRootJobNotFound})
	r := &Root{Client: ex}
	_, err := r.Run(context.Background(), adminAuth, "cli", nil)
	code(t, err, CodeRootTimeout)
	_, err = r.Start(context.Background(), adminAuth, "clean", nil)
	code(t, err, CodeRootBusy)
	_, err = r.Job(context.Background(), adminAuth, strings.Repeat("b", 64))
	code(t, err, CodeRootJobNotFound)
	for _, bad := range []string{"", "../x", strings.Repeat("a", 129)} {
		_, err = r.Run(context.Background(), adminAuth, bad, nil)
		code(t, err, CodeInvalidRequest)
	}
	_, err = r.Job(context.Background(), adminAuth, "x")
	code(t, err, CodeInvalidRequest)
}

func TestRootJobsStartListGetAndStop(t *testing.T) {
	id := strings.Repeat("c", 64)
	job := `{"job_id":"` + id + `","name":"clean","state":"running","requested_by":"alice","started_at":"2026-10-13T05:00:00Z","progress":{"percent":40,"message":"scanning","phase":"scan"},"output":""}`
	ended := `{"job_id":"` + id + `","name":"clean","state":"canceled","requested_by":"alice","started_at":"2026-10-13T05:00:00Z","ended_at":"2026-10-13T05:01:00Z","exit_code":0,"cancel_reason":"user","result":{"freed":3},"output":"done"}`
	ex, calls := rootExecutor(map[string]string{
		"root.start":     `{"job_id":"` + id + `","state":"running"}`,
		"root.jobs.list": `{"jobs":[` + job + `]}`,
		"root.jobs.get":  job,
		"root.jobs.stop": ended,
	})
	r := &Root{Client: ex}
	got, err := r.Start(context.Background(), adminAuth, "clean", map[string]int{"days": 7})
	if err != nil || got != id {
		t.Fatal(got, err)
	}
	jobs, err := r.Jobs(context.Background(), adminAuth)
	if err != nil || len(jobs) != 1 || jobs[0].ID != id || jobs[0].Ended() || *jobs[0].Progress.Percent != 40 || jobs[0].Progress.Phase != "scan" {
		t.Fatal(jobs, err)
	}
	stopped, err := r.Stop(context.Background(), adminAuth, id)
	if err != nil || !stopped.Ended() || stopped.State != RootJobCanceled || *stopped.ExitCode != 0 || stopped.CancelReason != "user" || string(stopped.Result) != `{"freed":3}` || stopped.EndedAt == nil || !stopped.StartedAt.Equal(time.Date(2026, 10, 13, 5, 0, 0, 0, time.UTC)) {
		t.Fatal(stopped, err)
	}
	if ops := []string{(*calls)[0].op, (*calls)[1].op, (*calls)[2].op}; !reflect.DeepEqual(ops, []string{"root.start", "root.jobs.list", "root.jobs.stop"}) || (*calls)[2].params["job_id"] != id {
		t.Fatal(*calls)
	}
}

// Wait polls a job until it ends and hands each answer to progress.
func TestRootWaitFollowsAJobToItsEnd(t *testing.T) {
	id := strings.Repeat("c", 64)
	states := []string{"running", "stopping", "succeeded"}
	n := 0
	ex := fakeExecutor(func(_ context.Context, op string, _ map[string]string, _, out any) error {
		s := states[min(n, len(states)-1)]
		n++
		return json.Unmarshal([]byte(`{"job_id":"`+id+`","name":"clean","state":"`+s+`","requested_by":"alice","started_at":"2026-10-13T05:00:00Z","output":""}`), out)
	})
	var seen []string
	job, err := (&Root{Client: ex}).Wait(context.Background(), adminAuth, id, time.Millisecond, func(j RootJob) { seen = append(seen, j.State) })
	if err != nil || job.State != RootJobSucceeded || !reflect.DeepEqual(seen, states) {
		t.Fatal(job, seen, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n = 0
	if _, err := (&Root{Client: ex}).Wait(ctx, adminAuth, id, time.Hour, nil); err == nil {
		t.Fatal("a canceled wait went on")
	}
}

// root.run waits for the program's two minutes; a request may carry 1 MiB.
func TestClientGivesRootRunItsDeadlineAndRequestSize(t *testing.T) {
	if timeoutFor("root.run", 20*time.Second) != RootRunTimeout || timeoutFor("root.start", 20*time.Second) != 20*time.Second || timeoutFor("root.run", time.Hour) != time.Hour {
		t.Fatal("deadlines")
	}
	c := executor(t, func(q Request) string { return `{"ok":true,"result":{}}` + "\n" })
	big := strings.Repeat("x", 512<<10)
	if err := c.Call(context.Background(), "root.run", adminAuth, map[string]any{"name": "cli", "params": map[string]string{"blob": big}}, nil); err != nil {
		t.Fatal(err)
	}
	code(t, c.Call(context.Background(), "files.list", adminAuth, map[string]any{"path": big}, nil), CodeExecutorTooLarge)
	code(t, c.Call(context.Background(), "root.start", adminAuth, map[string]any{"name": "cli", "params": map[string]string{"blob": big + big}}, nil), CodeExecutorTooLarge)
}

func TestLocalRootReadsThePackageLifecycle(t *testing.T) {
	c, ok := LocalRoot("MyApp").Client.(*Client)
	if !ok || c.Mailbox != "/var/packages/MyApp/var/executor" {
		t.Fatalf("root client %#v", LocalRoot("MyApp").Client)
	}
}
