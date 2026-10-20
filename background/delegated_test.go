package background

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPendingRevocationJournalBoundsAdmission(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "spool"))
	ctx := context.Background()
	e := s.withJournal(ctx, func(root *os.Root, _ *journal) (bool, error) {
		for i := 0; i < MaxHistory+MaxQueued; i++ {
			id := fmt.Sprintf("%032x", i)
			if e := writeAtomic(root, bindingName(id), DelegationBinding{JobID: id, DelegationID: "grant"}); e != nil {
				return false, e
			}
		}
		return false, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SubmitDelegated(ctx, SubmitRequest{Key: "key", Name: "file", Data: []byte("input"), Submitter: Submitter{User: "alice", UID: "1205"}}, func(string) (DelegationBinding, error) {
		t.Error("created grant despite cleanup backlog")
		return DelegationBinding{DelegationID: "grant"}, nil
	}, func(DelegationBinding) {})
	if !errors.Is(e, ErrQuota) {
		t.Fatalf("got %v, want revocation backlog quota", e)
	}
}
