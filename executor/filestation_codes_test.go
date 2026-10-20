package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStationErrorsBecomeSDKCodes(t *testing.T) {
	e, f, _ := setup(t)
	id := grant(t, e, "files.rename")
	os.WriteFile(filepath.Join(f.root, "old.txt"), nil, 0600)
	for dsm, want := range map[int]string{
		105: "PERMISSION_DENIED", 407: "PERMISSION_DENIED", 411: "PERMISSION_DENIED",
		408: "not-found", 412: "name-too-long", 413: "name-too-long", 414: "destination-exists",
		415: "NO_SPACE", 416: "NO_SPACE", 418: "invalid-path", 419: "invalid-path",
		401: "DSM_FAILED", 0: "DSM_FAILED",
	} {
		f.apiReply = func(string, map[string]any) (json.RawMessage, error) { return nil, &dsmError{Code: dsm} }
		if r := request(e, "files.rename", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/old.txt", "name": "new.txt"}); r.Code != want {
			t.Errorf("%d: %+v", dsm, r)
		}
	}
	f.apiReply = func(string, map[string]any) (json.RawMessage, error) { return nil, errors.New("DSM command") }
	if r := request(e, "files.rename", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/old.txt", "name": "new.txt"}); r.Code != "DSM_FAILED" {
		t.Fatal(r)
	}
}

func TestDSMKeepsTheCodeOfAnEmbeddedFileError(t *testing.T) {
	c := &cmdStub{reply: `{"success":true,"data":{"files":[{"path":"/home/a","error":{"code":414}}]}}`}
	d := &liveDSM{Command: c}
	_, err := d.API(context.Background(), "alice", "files.rename", map[string]any{"path": "/home/a", "name": "b"})
	var refused *dsmError
	if !errors.As(err, &refused) || refused.Code != 414 {
		t.Fatal(err)
	}
}

// DSM puts the cause in error.errors[]: copy into a taken name is
// {"error":{"code":1000,"errors":[{"code":1003,"path":…}]}}.
func TestDSMKeepsTheCodeOfTheErrorList(t *testing.T) {
	for reply, want := range map[string]int{
		`{"error":{"code":1000,"errors":[{"code":1003,"path":"/home/a"}]},"success":false}`: 1003,
		`{"error":{"code":1100,"errors":[{"code":414,"path":"/home/a"}]},"success":false}`:  414,
		`{"error":{"code":1100},"success":false}`:                                           1100,
	} {
		d := &liveDSM{Command: &cmdStub{reply: reply}}
		_, err := d.API(context.Background(), "alice", "files.createFolder", map[string]any{"path": "/home", "name": "a"})
		var refused *dsmError
		if !errors.As(err, &refused) || refused.Code != want {
			t.Errorf("%s: %v", reply, err)
		}
		if _, err = d.Start(context.Background(), "alice", "copy", map[string]any{"path": "/home/a", "dest_dir": "/home/b"}); !errors.As(err, &refused) || refused.Code != want {
			t.Errorf("start %s: %v", reply, err)
		}
	}
}

func TestTakenNamesAreDestinationExists(t *testing.T) {
	for _, code := range []int{414, 1003, 1004} {
		if got := fileStationCode(&dsmError{Code: code}); got != "destination-exists" {
			t.Errorf("%d: %s", code, got)
		}
	}
}
