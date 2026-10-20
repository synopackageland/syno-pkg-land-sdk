package background

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFreshOwnerAdmissionIsAtomicAcrossProcesses(t *testing.T) {
	_, dir := newStore(t)
	ctx := context.Background()
	owner := Submitter{User: "normal", UID: "1025"}
	req := SubmitRequest{Key: "one", Name: "a", Data: []byte("x"), Submitter: owner}
	fresh := NewStore(dir)
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := req
			r.Key = fmt.Sprint(i)
			_, _, err := fresh.Submit(ctx, r)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				accepted++
			} else if !errors.Is(err, ErrOwnerQueueFull) {
				t.Errorf("admission: %v", err)
			}
		}(i)
	}
	wg.Wait()
	raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("allowOrdinaryUsers")) || bytes.Contains(raw, []byte(`"enabled"`)) {
		t.Fatal("obsolete switches persisted")
	}
	if accepted != MaxOwnerPending {
		t.Fatalf("concurrent owner jobs %d", accepted)
	}
}
func TestOwnerByteQuotaIncludesReservedResult(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	submit(t, s, "one", make([]byte, MaxInputBytes))
	_, _, err := s.Submit(ctx, SubmitRequest{Key: "two", Name: "a", Data: make([]byte, MaxInputBytes), Submitter: admin})
	if !errors.Is(err, ErrOwnerQuota) {
		t.Fatalf("owner byte quota: %v", err)
	}
	if _, _, err = s.Submit(ctx, SubmitRequest{Key: "two", Name: "a", Data: make([]byte, MaxInputBytes-2*resultReserve), Submitter: admin}); err != nil {
		t.Fatal(err)
	}
}
func TestPurgeRunningJobDrainsWithoutResultAndAuditSurvives(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "one", []byte("x"))
	_, _ = s.Claim(ctx)
	audit, err := s.PurgeOwner(ctx, admin, "manager")
	if err != nil || audit.Draining != 1 || audit.Removed != 0 {
		t.Fatalf("%+v %v", audit, err)
	}
	if _, err := s.GetFor(ctx, job.ID, Access{Admin: true}); !errors.Is(err, ErrNotFound) {
		t.Fatal("purged job is visible")
	}
	if job, err := s.Get(ctx, job.ID); err != nil || !job.CancelRequested {
		t.Fatal("worker cannot see drain request")
	}
	if err := s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{Bytes: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("purged job retained after drain")
	}
	if _, err := os.Stat(filepath.Join(dir, "inputs", job.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("purged input remains")
	}
	fresh := NewStore(dir)
	if err := fresh.withJournal(ctx, func(_ *os.Root, j *journal) (bool, error) {
		if len(j.PurgeAudit) != 1 || j.PurgeAudit[0].Actor != "manager" {
			t.Fatal("audit lost")
		}
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestLegacyUnknownOwnerIsAdminOnlyAndRecoveryCompletesPurge(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "one", []byte("x"))
	_, _ = s.Claim(ctx)
	_ = s.withJournal(ctx, func(_ *os.Root, j *journal) (bool, error) { j.Jobs[0].Submitter.UID = ""; return true, nil })
	if list, _ := s.ListFor(ctx, Access{Owner: Submitter{User: "admin", UID: "1024"}}); len(list) != 0 {
		t.Fatal("legacy owner visible")
	}
	if list, _ := s.List(ctx); len(list) != 1 {
		t.Fatal("legacy owner hidden from admin")
	}
	if _, err := s.PurgeOwner(ctx, Submitter{User: "admin"}, "manager"); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(dir).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("crashed purge recovered a result")
	}
}
