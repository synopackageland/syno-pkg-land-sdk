package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type fakeReader struct {
	calls          int
	root, relative string
	limit          int64
	subject        Subject
	err            error
}

func (f *fakeReader) Read(_ context.Context, s Subject, root, relative string, limit int64) (string, int64, error) {
	f.calls++
	f.subject, f.root, f.relative, f.limit = s, root, relative, limit
	return "read-1", 3, f.err
}

func readSetup(t *testing.T) (*Executor, *fakeDSM, *fakeReader, string) {
	t.Helper()
	e, f, _ := setup(t)
	fr := &fakeReader{}
	e.Reader = fr
	os.MkdirAll(filepath.Join(f.root, "docs"), 0700)
	os.WriteFile(filepath.Join(f.root, "docs", "a.txt"), []byte("abc"), 0600)
	return e, f, fr, grant(t, e, "files.read")
}

func TestReadCopiesTheFileIntoStagingAsTheUser(t *testing.T) {
	e, f, fr, id := readSetup(t)
	r := request(e, "files.read", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/docs/a.txt"})
	b, _ := json.Marshal(r.Result)
	if !r.OK || string(b) != `{"size":3,"staging":"read-1"}` {
		t.Fatal(r, string(b))
	}
	if fr.subject.Username != "alice" || fr.root != f.root || fr.relative != "docs/a.txt" || fr.limit != maxReadBytes {
		t.Fatal(fr)
	}
	request(e, "files.read", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/docs/a.txt", "max_bytes": 2})
	if fr.limit != 2 {
		t.Fatal(fr.limit)
	}
}

func TestReadRefusesBadRequestsBeforeReading(t *testing.T) {
	e, _, fr, id := readSetup(t)
	for _, tc := range []struct {
		params map[string]any
		code   string
	}{
		{map[string]any{"path": "/etc/passwd"}, "SCOPE_DENIED"},
		{map[string]any{"path": "/home"}, "not-a-file"},
		{map[string]any{"path": "/home/docs/a.txt", "max_bytes": 0}, "INVALID_REQUEST"},
		{map[string]any{"path": "/home/docs/a.txt", "max_bytes": maxReadBytes + 1}, "INVALID_REQUEST"},
		{map[string]any{"path": "/home/docs/a.txt", "max_bytes": "1"}, "INVALID_REQUEST"},
	} {
		p := map[string]any{"delegation_id": id, "job_id": "job1"}
		for k, v := range tc.params {
			p[k] = v
		}
		if r := request(e, "files.read", p); r.Code != tc.code {
			t.Errorf("%v: %+v", tc.params, r)
		}
	}
	if fr.calls != 0 {
		t.Fatal(fr.calls)
	}
}

func TestReadReportsTheChildRefusal(t *testing.T) {
	e, _, fr, id := readSetup(t)
	for _, code := range []string{"limit-exceeded", "PERMISSION_DENIED", "not-found", "not-a-file", "invalid-path"} {
		fr.err = readRefusedError{code}
		if r := request(e, "files.read", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/docs/a.txt"}); r.Code != code {
			t.Errorf("%s: %+v", code, r)
		}
	}
	fr.err = os.ErrClosed
	if r := request(e, "files.read", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/docs/a.txt"}); r.Code != "READ_FAILED" {
		t.Fatal(r)
	}
}
