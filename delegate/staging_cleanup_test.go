package delegate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// committedStatus answers committed for in and reads it, which cleans up the
// staging source the way an App's status poll does.
func committedStatus(t *testing.T, before func(*Service, MoveParams)) (*Service, MoveParams) {
	t.Helper()
	s, in, e, _ := recoveringService(t)
	e.ops[in.JobID] = OperationResult{JobID: in.JobID, State: "committed", Receipt: &Receipt{Path: "/home/a", Size: 3}}
	e.params[in.JobID] = in
	reserveWithoutSending(t, s, in)
	if before != nil {
		before(s, in)
	}
	if out, err := s.OperationStatus(context.Background(), nil, OperationRef{in.DelegationID, in.JobID}); err != nil || out.State != "committed" {
		t.Fatal(out, err)
	}
	return s, in
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// A committed move leaves no empty staging folder behind; the
// staging root the App handed the SDK stays.
func TestCommitRemovesTheEmptyStagingFolder(t *testing.T) {
	s, _ := committedStatus(t, nil)
	if exists(t, filepath.Join(s.StagingDir, "exports")) || !exists(t, s.StagingDir) {
		t.Fatal("empty staging folder kept, or the staging root removed")
	}
}

// The executor usually removes the source itself at commit; its emptied
// folder goes too.
func TestCommitRemovesTheFolderTheExecutorEmptied(t *testing.T) {
	s, _ := committedStatus(t, func(s *Service, in MoveParams) {
		if err := os.Remove(filepath.Join(s.StagingDir, in.Staging)); err != nil {
			t.Fatal(err)
		}
	})
	if exists(t, filepath.Join(s.StagingDir, "exports")) {
		t.Fatal("empty staging folder kept")
	}
}

// A folder that still holds another file is the App's, and stays.
func TestCommitKeepsAStagingFolderInUse(t *testing.T) {
	s, _ := committedStatus(t, func(s *Service, _ MoveParams) {
		if err := os.WriteFile(filepath.Join(s.StagingDir, "exports", "b"), []byte("b"), 0600); err != nil {
			t.Fatal(err)
		}
	})
	if !exists(t, filepath.Join(s.StagingDir, "exports", "b")) {
		t.Fatal("folder in use removed")
	}
}

// Discarding a canceled move's source removes its emptied folder as well.
func TestDiscardStagingRemovesTheEmptyStagingFolder(t *testing.T) {
	s, in, _, _ := recoveringService(t)
	if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	ref := OperationRef{in.DelegationID, in.JobID}
	if _, err := s.CancelOperation(context.Background(), nil, ref); err != nil {
		t.Fatal(err)
	}
	if err := s.DiscardStaging(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if exists(t, filepath.Join(s.StagingDir, "exports")) {
		t.Fatal("empty staging folder kept")
	}
}
