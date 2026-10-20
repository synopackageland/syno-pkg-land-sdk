package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// grant creates a time-limited delegation on /home for ops.
func grant(t *testing.T, e *Executor, ops ...string) string {
	t.Helper()
	r := request(e, "delegate.create", map[string]any{"job_id": "job1", "ops": ops, "scope": "home", "path": "/home", "ttl_seconds": 60})
	if !r.OK {
		t.Fatalf("create: %+v", r)
	}
	b, _ := json.Marshal(r.Result)
	var d Delegation
	json.Unmarshal(b, &d)
	return d.ID
}

// mkdirReply creates the folder on disk the way File Station would and answers with path.
func mkdirReply(root, answered string) func(string, map[string]any) (json.RawMessage, error) {
	return func(_ string, args map[string]any) (json.RawMessage, error) {
		rel := args["path"].(string)[len("/home"):] + "/" + args["name"].(string)
		os.MkdirAll(filepath.Join(root, rel), 0700)
		b, _ := json.Marshal(map[string]any{"folders": []any{map[string]any{"path": answered, "name": args["name"]}}})
		return b, nil
	}
}

func TestCreateFolderWithParentsCreatesMissingAncestors(t *testing.T) {
	e, f, _ := setup(t)
	id := grant(t, e, "files.createFolder")
	f.apiReply = mkdirReply(f.root, "/home/a/b/c")
	r := request(e, "files.createFolder", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a/b", "name": "c", "parents": true})
	if !r.OK || f.lastArgs["parents"] != true {
		t.Fatal(r, f.lastArgs)
	}
	f.apiCalls = 0
	r = request(e, "files.createFolder", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/x/y", "name": "c"})
	if r.Code != "not-found" || f.apiCalls != 0 {
		t.Fatal("missing parent without parents", r)
	}
	if r := request(e, "files.createFolder", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home", "name": "c", "parents": "yes"}); r.Code != "INVALID_REQUEST" {
		t.Fatal(r)
	}
}

func TestCreateFolderWithParentsRefusesEscapingAncestors(t *testing.T) {
	e, f, _ := setup(t)
	id := grant(t, e, "files.createFolder")
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(f.root, "link"))
	os.WriteFile(filepath.Join(f.root, "file"), nil, 0600)
	f.apiReply = mkdirReply(f.root, "unused")
	for _, p := range []string{"/home/link/new", "/home/link"} {
		f.apiCalls = 0
		r := request(e, "files.createFolder", map[string]any{"delegation_id": id, "job_id": "job1", "path": p, "name": "c", "parents": true})
		if r.Code != "SCOPE_DENIED" || f.apiCalls != 0 {
			t.Errorf("%s: %+v calls=%d", p, r, f.apiCalls)
		}
	}
}

func TestCreateFolderChecksThePathFileStationReports(t *testing.T) {
	e, f, _ := setup(t)
	id := grant(t, e, "files.createFolder")
	for _, answered := range []string{"/home/a/other", "/etc/c", "/home/a/c/.."} {
		f.apiReply = mkdirReply(f.root, answered)
		r := request(e, "files.createFolder", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a", "name": "c", "parents": true})
		if r.OK || r.Code != "SCOPE_DENIED" {
			t.Errorf("%s: %+v", answered, r)
		}
	}
}

func TestDSMCreateFolderForwardsParents(t *testing.T) {
	for _, parents := range []bool{false, true} {
		c := &cmdStub{reply: `{"success":true,"data":{"folders":[{"path":"/home/a/c","name":"c"}]}}`}
		d := &liveDSM{Command: c}
		args := map[string]any{"path": "/home/a", "name": "c"}
		if parents {
			args["parents"] = true
		}
		if _, err := d.API(context.Background(), "alice", "files.createFolder", args); err != nil {
			t.Fatal(err)
		}
		want := "force_parent=false"
		if parents {
			want = "force_parent=true"
		}
		if !contains(c.args, want) {
			t.Fatal(c.args)
		}
	}
}

func TestCreateFolderInAFileIsNotADirectory(t *testing.T) {
	e, f, _ := setup(t)
	id := grant(t, e, "files.createFolder")
	os.WriteFile(filepath.Join(f.root, "file"), nil, 0600)
	f.apiReply = mkdirReply(f.root, "unused")
	for _, tc := range []struct {
		path    string
		parents bool
	}{{"/home/file", false}, {"/home/file", true}, {"/home/file/new", true}} {
		f.apiCalls = 0
		p := map[string]any{"delegation_id": id, "job_id": "job1", "path": tc.path, "name": "c"}
		if tc.parents {
			p["parents"] = true
		}
		if r := request(e, "files.createFolder", p); r.Code != "destination-not-directory" || f.apiCalls != 0 {
			t.Errorf("%+v: %+v calls=%d", tc, r, f.apiCalls)
		}
	}
}
