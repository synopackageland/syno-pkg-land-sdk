package delegate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

type sentOp struct {
	Op     string
	Params map[string]any
}

// recordingExecutor answers each op with replies[op] and records what was sent.
func recordingExecutor(t *testing.T, replies map[string]string) (*Client, func() []sentOp) {
	var mu sync.Mutex
	var sent []sentOp
	c := executor(t, func(q Request) string {
		var p map[string]any
		json.Unmarshal(q.Params, &p)
		mu.Lock()
		sent = append(sent, sentOp{q.Op, p})
		mu.Unlock()
		if r, ok := replies[q.Op]; ok {
			return r + "\n"
		}
		return `{"ok":true,"result":{}}` + "\n"
	})
	return c, func() []sentOp { mu.Lock(); defer mu.Unlock(); return append([]sentOp(nil), sent...) }
}

func TestJobFilesSendTheExecutorFileOperations(t *testing.T) {
	task := `{"ok":true,"result":{"task_id":"t1","state":"running"}}`
	c, sent := recordingExecutor(t, map[string]string{
		"files.copy": task, "files.move": task, "files.delete": task,
		"files.taskStatus": `{"ok":true,"result":{"task_id":"t1","state":"failed","progress":0.5,"error":"destination-exists"}}`,
		"files.taskCancel": `{"ok":true,"result":{"task_id":"t1","state":"canceled"}}`,
	})
	f := JobContext{Client: c, JobID: "j", DelegationID: "d"}.Files()
	ctx := context.Background()
	if e := f.CreateFolderAll(ctx, "/home/a/b", "c"); e != nil {
		t.Fatal(e)
	}
	if e := f.Rename(ctx, "/home/a.txt", "b.txt"); e != nil {
		t.Fatal(e)
	}
	for _, start := range []func() (TaskState, error){
		func() (TaskState, error) { return f.Copy(ctx, "/home/a.txt", "/home/dst", Conflict("")) },
		func() (TaskState, error) { return f.Move(ctx, "/home/a.txt", "/home/dst", ConflictOverwrite) },
		func() (TaskState, error) { return f.Delete(ctx, "/home/src", DeleteOptions{Recursive: true}) },
	} {
		if s, e := start(); e != nil || s.TaskID != "t1" || s.State != "running" {
			t.Fatal(s, e)
		}
	}
	if s, e := f.TaskStatus(ctx, "t1"); e != nil || s.State != "failed" || s.Progress != 0.5 || s.Error != "destination-exists" {
		t.Fatal(s, e)
	}
	if s, e := f.CancelTask(ctx, "t1"); e != nil || s.State != "canceled" {
		t.Fatal(s, e)
	}
	want := []sentOp{
		{"files.createFolder", map[string]any{"path": "/home/a/b", "name": "c", "parents": true}},
		{"files.rename", map[string]any{"path": "/home/a.txt", "name": "b.txt"}},
		{"files.copy", map[string]any{"path": "/home/a.txt", "dest_dir": "/home/dst"}},
		{"files.move", map[string]any{"path": "/home/a.txt", "dest_dir": "/home/dst", "conflict": "overwrite"}},
		{"files.delete", map[string]any{"path": "/home/src", "recursive": true}},
		{"files.taskStatus", map[string]any{"task_id": "t1"}},
		{"files.taskCancel", map[string]any{"task_id": "t1"}},
	}
	got := sent()
	for i := range got {
		delete(got[i].Params, "job_id")
		delete(got[i].Params, "delegation_id")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v\n%v", got, want)
	}
}

func TestJobFilesRefuseInvalidRequestsLocally(t *testing.T) {
	c, sent := recordingExecutor(t, nil)
	f := JobContext{Client: c, JobID: "j", DelegationID: "d"}.Files()
	ctx := context.Background()
	code(t, f.Rename(ctx, "/home/a", "x/y"), CodeInvalidRequest)
	code(t, f.Rename(ctx, "relative", "x"), CodeInvalidRequest)
	_, e := f.Copy(ctx, "/home/a", "/home/b", Conflict("rename"))
	code(t, e, CodeInvalidRequest)
	_, e = f.Delete(ctx, "/home/../etc", DeleteOptions{})
	code(t, e, CodeInvalidRequest)
	_, e = f.TaskStatus(ctx, "")
	code(t, e, CodeInvalidRequest)
	_, e = f.Read(ctx, "/home/a", MaxDownloadBytes+1)
	code(t, e, CodeInvalidRequest)
	if len(sent()) != 0 {
		t.Fatal(sent())
	}
}

func TestJobFilesReadTakesAndRemovesTheStagedCopy(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	os.WriteFile(filepath.Join(dir, "read-1"), []byte("abc"), 0600)
	c, sent := recordingExecutor(t, map[string]string{"files.read": `{"ok":true,"result":{"staging":"read-1","size":3}}`})
	f := JobContext{Client: c, JobID: "j", DelegationID: "d", StagingDir: dir}.Files()
	b, e := f.Read(context.Background(), "/home/a.txt", 10)
	if e != nil || string(b) != "abc" || sent()[0].Params["max_bytes"] != float64(10) {
		t.Fatal(string(b), e, sent())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("staged copy left behind", entries)
	}
	for _, reply := range []string{
		`{"ok":true,"result":{"staging":"../escape","size":3}}`,
		`{"ok":true,"result":{"staging":"read-2","size":3}}`,
		`{"ok":true,"result":{"staging":"read-3","size":2}}`,
	} {
		os.WriteFile(filepath.Join(dir, "read-3"), []byte("abc"), 0600)
		c, _ := recordingExecutor(t, map[string]string{"files.read": reply})
		f := JobContext{Client: c, JobID: "j", DelegationID: "d", StagingDir: dir}.Files()
		_, e := f.Read(context.Background(), "/home/a.txt", 10)
		code(t, e, "EXECUTOR_PROTOCOL")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("mismatched staged copy left behind", entries)
	}
}

func TestServiceGrantsTheNewFileOperations(t *testing.T) {
	c, sent := recordingExecutor(t, map[string]string{"delegate.create": `{"ok":true,"result":{"delegation_id":"d1","subject":{"username":"alice","uid":1205},"expires_at":"2030-01-01T00:00:00Z"}}`})
	s := service(t, c)
	out := rpc(t, s, "alice", "delegate.create", `{"job_id":"j","ops":["files.read","files.rename","files.copy","files.move","files.delete","files.list"],"path":"/home","ttl_seconds":600}`)
	if out["success"] != true || len(sent()) != 1 {
		t.Fatal(out)
	}
	out = rpc(t, s, "alice", "delegate.create", `{"job_id":"j","ops":["files.taskStatus"],"path":"/home","ttl_seconds":600}`)
	if out["success"] != false {
		t.Fatal("implied operation granted directly")
	}
}

func TestJobFilesReadCreatesTheStagingFolderFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "executor-staging")
	c, sent := recordingExecutor(t, map[string]string{"files.read": `{"ok":true,"result":{"staging":"read-1","size":0}}`})
	f := JobContext{Client: c, JobID: "j", DelegationID: "d", StagingDir: dir}.Files()
	f.Read(context.Background(), "/home/a.txt", 10)
	info, e := os.Stat(dir)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 || len(sent()) != 1 {
		t.Fatal(info, e, sent())
	}
}

func TestClientValidationUsesTheSpecCode(t *testing.T) {
	c, _ := recordingExecutor(t, nil)
	f := JobContext{Client: c, JobID: "j", DelegationID: "d", StagingDir: t.TempDir()}.Files()
	code(t, f.Rename(context.Background(), "/home/a", "../b"), CodeInvalidRequest)
	_, e := f.Read(context.Background(), "/home/a", 0)
	code(t, e, CodeInvalidRequest)
}
