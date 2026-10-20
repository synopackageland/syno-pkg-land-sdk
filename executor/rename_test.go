package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func renameReply(answered string) func(string, map[string]any) (json.RawMessage, error) {
	return func(string, map[string]any) (json.RawMessage, error) {
		b, _ := json.Marshal(map[string]any{"files": []any{map[string]any{"path": answered, "name": filepath.Base(answered)}}})
		return b, nil
	}
}

func TestRenameRenamesInPlaceWithinScope(t *testing.T) {
	e, f, _ := setup(t)
	id := grant(t, e, "files.rename")
	os.WriteFile(filepath.Join(f.root, "old.txt"), nil, 0600)
	os.WriteFile(filepath.Join(f.root, "new.txt"), nil, 0600) // File Station has renamed it
	f.apiReply = renameReply("/home/new.txt")
	r := request(e, "files.rename", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/old.txt", "name": "new.txt"})
	if !r.OK || f.lastOp != "files.rename" || f.lastArgs["path"] != "/home/old.txt" || f.lastArgs["name"] != "new.txt" {
		t.Fatal(r, f.lastOp, f.lastArgs)
	}
}

func TestRenameRefusesUnsafeRequestsBeforeCallingDSM(t *testing.T) {
	e, f, _ := setup(t)
	id := grant(t, e, "files.rename")
	os.WriteFile(filepath.Join(f.root, "old.txt"), nil, 0600)
	f.apiReply = renameReply("/home/x")
	for _, tc := range []struct{ path, name, code string }{
		{"/home/old.txt", "../x", "INVALID_REQUEST"},
		{"/home/old.txt", "a/b", "INVALID_REQUEST"},
		{"/home/old.txt", "", "INVALID_REQUEST"},
		{"/home", "renamed", "SCOPE_DENIED"},
		{"/etc/passwd", "x", "SCOPE_DENIED"},
		{"/home/missing", "x", "not-found"},
	} {
		f.apiCalls = 0
		r := request(e, "files.rename", map[string]any{"delegation_id": id, "job_id": "job1", "path": tc.path, "name": tc.name})
		if r.Code != tc.code || f.apiCalls != 0 {
			t.Errorf("%+v: %+v calls=%d", tc, r, f.apiCalls)
		}
	}
}

func TestRenameChecksThePathFileStationReports(t *testing.T) {
	e, f, _ := setup(t)
	id := grant(t, e, "files.rename")
	os.WriteFile(filepath.Join(f.root, "old.txt"), nil, 0600)
	os.WriteFile(filepath.Join(f.root, "other.txt"), nil, 0600)
	for _, answered := range []string{"/home/other.txt", "/etc/new.txt"} {
		f.apiReply = renameReply(answered)
		if r := request(e, "files.rename", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/old.txt", "name": "new.txt"}); r.Code != "SCOPE_DENIED" {
			t.Errorf("%s: %+v", answered, r)
		}
	}
}

func TestDSMRenameUsesFileStationRename(t *testing.T) {
	c := &cmdStub{reply: `{"success":true,"data":{"files":[{"path":"/home/new.txt","name":"new.txt"}]}}`}
	d := &liveDSM{Command: c}
	if _, err := d.API(context.Background(), "alice", "files.rename", map[string]any{"path": "/home/old.txt", "name": "new.txt"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"api=SYNO.FileStation.Rename", "method=rename", "version=2", "runner=alice", `path=["/home/old.txt"]`, `name=["new.txt"]`} {
		if !contains(c.args, want) {
			t.Fatal(want, c.args)
		}
	}
}
