package main

import (
	"context"
	"errors"
	"testing"
)

func TestDSMStartsCopyMoveAndDeleteTasks(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		params map[string]any
		want   []string
	}{
		{"copy", map[string]any{"path": "/home/a", "dest_dir": "/home/b"}, []string{"api=SYNO.FileStation.CopyMove", "method=start", "version=3", `path=["/home/a"]`, `dest_folder_path="/home/b"`, "remove_src=false"}},
		{"move", map[string]any{"path": "/home/a", "dest_dir": "/home/b", "overwrite": true}, []string{"api=SYNO.FileStation.CopyMove", "remove_src=true", "overwrite=true"}},
		{"delete", map[string]any{"path": "/home/a", "recursive": true}, []string{"api=SYNO.FileStation.Delete", "method=start", "version=2", `path=["/home/a"]`, "recursive=true", "accurate_progress=false"}},
	} {
		c := &cmdStub{reply: `{"success":true,"data":{"taskid":"FileStation_1"}}`}
		d := &liveDSM{Command: c}
		id, err := d.Start(context.Background(), "alice", tc.kind, tc.params)
		if err != nil || id != "FileStation_1" || !contains(c.args, "runner=alice") {
			t.Fatal(tc.kind, id, err, c.args)
		}
		for _, w := range tc.want {
			if !contains(c.args, w) {
				t.Errorf("%s: missing %s in %v", tc.kind, w, c.args)
			}
		}
		if tc.kind == "copy" && contains(c.args, "overwrite=true") {
			t.Error("copy without overwrite asked to overwrite")
		}
	}
	d := &liveDSM{Command: &cmdStub{reply: `{"success":true,"data":{}}`}}
	if _, err := d.Start(context.Background(), "alice", "copy", map[string]any{"path": "/home/a", "dest_dir": "/home/b"}); err == nil {
		t.Fatal("start without a task id")
	}
	if _, err := d.Start(context.Background(), "alice", "chmod", map[string]any{}); err == nil {
		t.Fatal("unknown kind")
	}
}

func TestDSMTaskStatusReportsProgressPathsAndErrors(t *testing.T) {
	c := &cmdStub{reply: `{"success":true,"data":{"finished":false,"progress":0.25,"path":"/home/a","dest_folder_path":"/home/b","processed_size":1,"total":4}}`}
	d := &liveDSM{Command: c}
	got, err := d.Status(context.Background(), "alice", "move", "FileStation_1")
	if err != nil || got.Finished || got.Progress != 0.25 || len(got.Paths) != 2 || !contains(c.args, `taskid="FileStation_1"`) || !contains(c.args, "method=status") {
		t.Fatal(got, err, c.args)
	}
	c.reply = `{"success":true,"data":{"finished":true,"progress":1,"path":["/home/a"],"processing_path":"/home/a/x","error":{"code":416}}}`
	got, err = d.Status(context.Background(), "alice", "delete", "FileStation_1")
	if err != nil || !got.Finished || got.Code != 416 || len(got.Paths) != 2 || !contains(c.args, "api=SYNO.FileStation.Delete") {
		t.Fatal(got, err)
	}
	c.reply = `{"success":false,"error":{"code":599}}`
	_, err = d.Status(context.Background(), "alice", "copy", "FileStation_1")
	var refused *dsmError
	if !errors.As(err, &refused) || refused.Code != 599 {
		t.Fatal(err)
	}
}

func TestDSMStopAcceptsASuccessWithoutData(t *testing.T) {
	c := &cmdStub{reply: `{"success":true}`}
	d := &liveDSM{Command: c}
	if err := d.Stop(context.Background(), "alice", "copy", "FileStation_1"); err != nil || !contains(c.args, "method=stop") || !contains(c.args, "runner=alice") {
		t.Fatal(err, c.args)
	}
	c.reply = `{"success":false,"error":{"code":599}}`
	if err := d.Stop(context.Background(), "alice", "copy", "FileStation_1"); err == nil {
		t.Fatal("refused stop")
	}
}

func TestDSMRecycleBinReadsTheShareSetting(t *testing.T) {
	for reply, want := range map[string]bool{
		`{"success":true,"data":{"name":"homes","enable_recycle_bin":true}}`:  true,
		`{"success":true,"data":{"name":"homes","enable_recycle_bin":false}}`: false,
	} {
		c := &cmdStub{reply: reply}
		d := &liveDSM{Command: c}
		got, err := d.RecycleBin(context.Background(), Subject{Username: "alice"}, "home")
		if err != nil || got != want || !contains(c.args, `name="homes"`) || contains(c.args, "runner=alice") {
			t.Fatal(reply, got, err, c.args)
		}
	}
	c := &cmdStub{reply: `{"success":true,"data":{"name":"media"}}`}
	d := &liveDSM{Command: c}
	if _, err := d.RecycleBin(context.Background(), Subject{}, "shared:media"); err == nil || !contains(c.args, `name="media"`) {
		t.Fatal("missing setting must fail closed", err)
	}
}

// A finished task lists the paths DSM did not process in errors[]; a task that
// skipped a taken name reports skipstatus. Neither is a success.
func TestDSMTaskStatusReadsTheErrorListAndSkips(t *testing.T) {
	for reply, want := range map[string]int{
		`{"success":true,"data":{"finished":true,"progress":1,"path":"/home/ro/f.txt","errors":[{"code":407,"path":"/home/ro/f.txt"}]}}`:      407,
		`{"success":true,"data":{"finished":true,"progress":1,"path":"","status":"FAIL","errors":[{"code":1008,"path":"/home/many/f.bin"}]}}`: 1008,
		`{"success":true,"data":{"finished":true,"progress":1,"path":"","status":"FAIL"}}`:                                                    401,
		`{"success":true,"data":{"finished":true,"progress":1,"path":"","skipstatus":{"status":"all"},"status":"PROCESSING","total":0}}`:      1003,
		`{"success":true,"data":{"finished":true,"progress":1,"path":"/home/a","status":"PROCESSING","processed_num":1,"total":1}}`:           0,
	} {
		d := &liveDSM{Command: &cmdStub{reply: reply}}
		got, err := d.Status(context.Background(), "alice", "copy", "FileStation_1")
		if err != nil || got.Code != want {
			t.Errorf("%s: %+v %v", reply, got, err)
		}
	}
}
