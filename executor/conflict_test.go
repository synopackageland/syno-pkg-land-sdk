package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRenameCandidateUsesDSMStyle(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		want string
	}{
		{"01.flac", 1, "01 (1).flac"},
		{"01.flac", 12, "01 (12).flac"},
		{"README", 2, "README (2)"},
		{".bashrc", 1, ".bashrc (1)"},
		{"a.tar.gz", 1, "a.tar (1).gz"},
		{"Album/CD1/01.flac", 3, "Album/CD1/01 (3).flac"},
		{"Album.v2/track", 1, "Album.v2/track (1)"},
	} {
		if got := renameCandidate(tc.name, tc.n); got != tc.want {
			t.Errorf("%q %d: %q", tc.name, tc.n, got)
		}
	}
}

func TestReceiptNameAcceptsOnlyARenameOfTheRequest(t *testing.T) {
	op := Operation{Name: "Album/01.flac", Conflict: "rename"}
	for got, want := range map[string]string{
		"":                    "Album/01.flac",
		"Album/01 (1).flac":   "Album/01 (1).flac",
		"Album/01 (99).flac":  "Album/01 (99).flac",
		"Album/02.flac":       "Album/01.flac",
		"Other/01 (1).flac":   "Album/01.flac",
		"Album/01 (0).flac":   "Album/01.flac",
		"Album/01 (100).flac": "Album/01.flac",
	} {
		if name := receiptName(op, MoveReceipt{Name: got}); name != want {
			t.Errorf("%q: %q", got, name)
		}
	}
	op.Conflict = "overwrite"
	if name := receiptName(op, MoveReceipt{Name: "Album/01 (1).flac"}); name != op.Name {
		t.Fatal("only rename may change the name", name)
	}
}

// Rename answers with the name the child chose; the receipt reports it.
type renamingWriter struct{ barrierMoveWriter }

func (w *renamingWriter) Move(ctx context.Context, s Subject, op Operation, d Delegation, guard func(func() (MoveReceipt, error)) (MoveReceipt, error)) (MoveReceipt, error) {
	return guard(func() (MoveReceipt, error) { return MoveReceipt{Name: renameCandidate(op.Name, 1)}, nil })
}

func TestMoveConflictPolicyIsAdmittedStoredAndReceipted(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	e.Writer = &renamingWriter{}
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	p := moveParams(id, "renamed")
	p["conflict"] = "rename"
	if r := offline(e, "files.moveFromStaging", p); !r.OK {
		t.Fatal(r)
	}
	op := waitState(t, e, "renamed", "committed")
	if op.Conflict != "rename" || op.Path != "/home/out (1).csv" {
		t.Fatal(op)
	}
	p["conflict"] = "overwrite"
	if r := offline(e, "files.moveFromStaging", p); r.Code != "operation-conflict" {
		t.Fatal("same job with another policy", r)
	}
	delete(p, "conflict")
	if r := offline(e, "files.moveFromStaging", p); r.Code != "operation-conflict" {
		t.Fatal("same job without the policy", r)
	}
	for _, bad := range []any{"replace", "", nil, 1} {
		q := moveParams(id, "bad-policy")
		q["conflict"] = bad
		if r := offline(e, "files.moveFromStaging", q); r.Code != "INVALID_REQUEST" {
			t.Errorf("%v: %+v", bad, r)
		}
	}
}

func TestMoveWithoutConflictKeepsFailPolicy(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	e.Writer = &renamingWriter{}
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(id, "plain")); !r.OK {
		t.Fatal(r)
	}
	op := waitState(t, e, "plain", "committed")
	if op.Conflict != "" || op.Path != "/home/out.csv" {
		t.Fatal(op)
	}
}

type conflictWriter struct{ got string }

func (w *conflictWriter) Write(ctx context.Context, _ Subject, _, _, name, _ string) (MoveReceipt, error) {
	w.got, _ = ctx.Value(writeConflictKey{}).(string)
	return MoveReceipt{Name: renameCandidate(name, 1)}, nil
}

func TestLegacyWritePassesConflictAndReportsRenamedPath(t *testing.T) {
	e, _, _ := setup(t)
	w := &conflictWriter{}
	e.Writer = w
	id := create(t, e)
	p := map[string]any{"delegation_id": id, "job_id": "job1", "staging": "a", "dest_dir": "/home", "name": "new.txt", "conflict": "rename"}
	r := request(e, "files.write", p)
	b, _ := json.Marshal(r.Result)
	if !r.OK || w.got != "rename" || string(b) != `{"path":"/home/new (1).txt"}` {
		t.Fatal(r, w.got, string(b))
	}
	p["conflict"] = "skip"
	if r := request(e, "files.write", p); r.Code != "INVALID_REQUEST" {
		t.Fatal(r)
	}
	delete(p, "conflict")
	if r := request(e, "files.write", p); !r.OK || w.got != "" {
		t.Fatal("default policy", r, w.got)
	}
}
