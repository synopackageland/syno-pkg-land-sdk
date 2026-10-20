package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func realTestPath(t *testing.T, p string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func makeTestDir(p string) error { return os.Mkdir(p, 0700) }

// DSM links /var/packages/<pkg>/target to the volume. Every platform must
// refuse a symlinked ancestor so tests catch callers that skip EvalSymlinks.
func TestOpenRegularRefusesASymlinkedAncestor(t *testing.T) {
	dir := realTestPath(t, t.TempDir())
	os.MkdirAll(filepath.Join(dir, "appstore"), 0755)
	os.WriteFile(filepath.Join(dir, "appstore", "executor"), []byte("x"), 0755)
	os.Symlink(filepath.Join(dir, "appstore"), filepath.Join(dir, "target"))
	if f, err := openRegular(filepath.Join(dir, "target", "executor")); err == nil {
		f.Close()
		t.Fatal("symlinked ancestor accepted")
	}
	f, err := openRegular(filepath.Join(dir, "appstore", "executor"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// A path that does not exist is not-found; only a path that resolves, or
// whose nearest existing folder resolves, outside the grant is SCOPE_DENIED.
func TestMissingPathsAreNotFound(t *testing.T) {
	e, f, _ := setup(t)
	id := grant(t, e, "files.info", "files.list", "files.rename", "files.read")
	os.Symlink(t.TempDir(), filepath.Join(f.root, "link"))
	os.WriteFile(filepath.Join(f.root, "file"), nil, 0600)
	f.apiReply = func(string, map[string]any) (json.RawMessage, error) { return nil, errors.New("unexpected DSM call") }
	for _, tc := range []struct{ op, path, code string }{
		{"files.info", "/home/missing", "not-found"},
		{"files.list", "/home/missing/deeper", "not-found"},
		{"files.read", "/home/missing", "not-found"},
		{"files.info", "/home/file/x", "not-found"},
		{"files.info", "/home/link/missing", "SCOPE_DENIED"},
		{"files.info", "/etc/missing", "SCOPE_DENIED"},
	} {
		p := map[string]any{"delegation_id": id, "job_id": "job1", "path": tc.path}
		if r := request(e, tc.op, p); r.Code != tc.code {
			t.Errorf("%+v: %+v", tc, r)
		}
	}
	if r := request(e, "files.rename", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/missing", "name": "x"}); r.Code != "not-found" {
		t.Error(r)
	}
}
