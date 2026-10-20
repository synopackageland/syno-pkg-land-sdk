package background

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func scheduledStore(t *testing.T) (*Store, *time.Time, Job, Schedule) {
	t.Helper()
	s, _ := newStore(t)
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	source := submit(t, s, "source", []byte("copy"))
	_, _ = s.Claim(context.Background())
	if err := s.Finish(context.Background(), source.ID, Outcome{State: StateSucceeded, Result: &Result{SHA256: digest([]byte("copy")), Bytes: 4}}); err != nil {
		t.Fatal(err)
	}
	sc, _, err := s.CreateSchedule(context.Background(), ScheduleRequest{Key: "schedule", JobID: source.ID, IntervalSeconds: 3600, Consent: true}, Access{Owner: admin, Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, &now, source, sc
}
func TestScheduleDurablePrivateCopyAndConsent(t *testing.T) {
	s, now, source, sc := scheduledStore(t)
	ctx := context.Background()
	list, err := NewStore(s.dir).Schedules(ctx, Access{Owner: admin})
	if err != nil || len(list) != 1 || list[0].InputDigest != source.InputDigest || !list[0].NextAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("durable %+v %v", list, err)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, inputs, sc.ID))
	if err != nil || string(b) != "copy" {
		t.Fatalf("copy %q %v", b, err)
	}
	for _, r := range []ScheduleRequest{{Key: "x", JobID: source.ID, IntervalSeconds: 3600}, {Key: "x", JobID: source.ID, IntervalSeconds: 1, Consent: true}, {Key: "x", JobID: "../journal.json", IntervalSeconds: 3600, Consent: true}} {
		if _, _, err := s.CreateSchedule(ctx, r, Access{Owner: admin, Admin: true}); err == nil {
			t.Fatalf("admitted %+v", r)
		}
	}
	again, dup, err := s.CreateSchedule(ctx, ScheduleRequest{Key: "schedule", JobID: source.ID, IntervalSeconds: 3600, Consent: true}, Access{Owner: admin, Admin: true})
	if err != nil || !dup || again.ID != sc.ID {
		t.Fatalf("retry %+v %v %v", again, dup, err)
	}
}
func TestScheduleConcurrentTwoTriggersNoReplayRestartMissed(t *testing.T) {
	s, now, _, sc := scheduledStore(t)
	ctx := context.Background()
	for round := 1; round <= 2; round++ {
		*now = now.Add(time.Hour)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := s.Tick(ctx); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		jobs, _ := s.List(ctx)
		n := 0
		for _, j := range jobs {
			if j.ScheduleID == sc.ID {
				n++
			}
		}
		if n != round {
			t.Fatalf("round %d got %d", round, n)
		}
		j, err := s.Claim(ctx)
		if err != nil || j == nil {
			t.Fatal(err)
		}
		if !j.ScheduledAt.Equal(*now) {
			t.Fatal("trigger time")
		}
		if err := s.Finish(ctx, j.ID, Outcome{State: StateSucceeded, Result: &Result{SHA256: j.InputDigest, Bytes: 4}}); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.List(ctx)
	*now = now.Add(3*time.Hour + time.Minute)
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.Tick(ctx)
	after, _ := s.List(ctx)
	if len(after) != len(before) {
		t.Fatal("restart replayed")
	}
	list, _ := s.Schedules(ctx, Access{Admin: true})
	if list[0].Missed != 3 || !list[0].NextAt.After(*now) {
		t.Fatalf("missed %+v", list)
	}
	*now = now.Add(-4 * time.Hour)
	_ = s.Tick(ctx)
	after, _ = s.List(ctx)
	if len(after) != len(before) {
		t.Fatal("backward clock replay")
	}
}
func TestScheduleIsolationDisableDeleteRevision(t *testing.T) {
	s, now, source, sc := scheduledStore(t)
	ctx := context.Background()
	other := Access{Owner: Submitter{User: "fixtureuser2", UID: "1026"}}
	if list, _ := s.Schedules(ctx, other); len(list) != 0 {
		t.Fatal("leak")
	}
	if _, err := s.SetSchedule(ctx, sc.ID, false, other); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.DeleteSchedule(ctx, sc.ID, other); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, _, err := s.CreateSchedule(ctx, ScheduleRequest{Key: "steal", JobID: source.ID, IntervalSeconds: 3600, Consent: true}, other); err == nil {
		t.Fatal("cross-owner")
	}
	*now = now.Add(time.Hour)
	_ = s.Tick(ctx)
	job, _ := s.Claim(ctx)
	if _, err := s.SetSchedule(ctx, sc.ID, false, Access{Owner: admin}); err != nil {
		t.Fatal(err)
	}
	_ = s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{Bytes: 4}})
	got, _ := s.Get(ctx, job.ID)
	if got.State != StateCanceled || got.Result != nil {
		t.Fatal("disable lost")
	}
	*now = now.Add(time.Hour)
	_ = s.Tick(ctx)
	jobs, _ := s.List(ctx)
	if len(jobs) != 2 {
		t.Fatal("disabled trigger")
	}
	if _, err := s.SetSchedule(ctx, sc.ID, true, Access{Owner: admin}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	s.revision++
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Schedules(ctx, Access{Admin: true})
	if list[0].Enabled || list[0].Reason != "worker-changed" {
		t.Fatal(list)
	}
	if _, err := s.SetSchedule(ctx, sc.ID, true, Access{Admin: true, Owner: admin}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.Schedules(ctx, Access{Admin: true})
	if list[0].WorkerRevision != s.revision {
		t.Fatal("revision")
	}
	if err := s.DeleteSchedule(ctx, sc.ID, Access{Owner: admin}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, inputs, sc.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("copy remains")
	}
}
func TestScheduleBoundsQuotasAndModeRevocation(t *testing.T) {
	s, now, source, _ := scheduledStore(t)
	ctx := context.Background()
	for i := 1; i < MaxSchedules; i++ {
		if _, _, err := s.CreateSchedule(ctx, ScheduleRequest{Key: fmt.Sprint(i), JobID: source.ID, IntervalSeconds: 3600, Consent: true}, Access{Owner: admin, Admin: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.CreateSchedule(ctx, ScheduleRequest{Key: "overflow", JobID: source.ID, IntervalSeconds: 3600, Consent: true}, Access{Owner: admin, Admin: true}); !errors.Is(err, ErrScheduleFull) {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	_ = s.Tick(ctx)
	jobs, _ := s.List(ctx)
	n := 0
	for _, j := range jobs {
		if !j.State.terminal() {
			n++
		}
	}
	if n != MaxOwnerPending {
		t.Fatalf("pending %d", n)
	}
	list, _ := s.Schedules(ctx, Access{Admin: true})
	missed := uint64(0)
	for _, sc := range list {
		missed += sc.Missed
	}
	if missed != 6 {
		t.Fatalf("missed %d", missed)
	}
	for _, sc := range list {
		if _, err := s.SetSchedule(ctx, sc.ID, false, Access{Admin: true}); err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(time.Hour)
	_ = s.Tick(ctx)
	list, _ = s.Schedules(ctx, Access{Admin: true})
	for _, sc := range list {
		if sc.Enabled {
			t.Fatal("revived")
		}
	}
}
func TestScheduleLateLegacyTamperPurge(t *testing.T) {
	s, now, source, sc := scheduledStore(t)
	ctx := context.Background()
	*now = now.Add(time.Hour + ScheduleWindow + time.Second)
	_ = s.Tick(ctx)
	jobs, _ := s.List(ctx)
	if len(jobs) != 1 {
		t.Fatal("catchup")
	}
	list, _ := s.Schedules(ctx, Access{Admin: true})
	if list[0].Missed != 1 || list[0].Reason != "missed" {
		t.Fatal(list)
	}
	_ = os.Remove(filepath.Join(s.dir, inputs, source.ID))
	if _, _, err := s.CreateSchedule(ctx, ScheduleRequest{Key: "legacy", JobID: source.ID, IntervalSeconds: 3600, Consent: true}, Access{Owner: admin, Admin: true}); !errors.Is(err, ErrInputUnavailable) {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(s.dir, inputs, sc.ID), []byte("evil"), 0600)
	*now = list[0].NextAt
	_ = s.Tick(ctx)
	list, _ = s.Schedules(ctx, Access{Admin: true})
	if list[0].Enabled || list[0].Reason != "input-unavailable" {
		t.Fatal(list)
	}
	_, err := s.PurgeOwner(ctx, admin, "admin")
	if err != nil {
		t.Fatal(err)
	}
	list, _ = s.Schedules(ctx, Access{Admin: true})
	if len(list) != 0 {
		t.Fatal("purge schedule")
	}
}

func TestScheduleReservesFourManualQueueSlots(t *testing.T) {
	s, now, _, sc := scheduledStore(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		_, _, err := s.Submit(ctx, SubmitRequest{Key: fmt.Sprint(i), Name: "manual", Data: []byte("x"), Submitter: Submitter{User: "other", UID: fmt.Sprint(2000 + i)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(time.Hour)
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Schedules(ctx, Access{Admin: true})
	if list[0].ID != sc.ID || list[0].Reason != "queue-full" || list[0].Missed != 1 {
		t.Fatal(list)
	}
	for i := 12; i < 16; i++ {
		_, _, err := s.Submit(ctx, SubmitRequest{Key: fmt.Sprint(i), Name: "manual", Data: []byte("x"), Submitter: Submitter{User: "other", UID: fmt.Sprint(2000 + i)}})
		if err != nil {
			t.Fatalf("reserved slot: %v", err)
		}
	}
}
func TestRetainedCopyQuotaHistoryAndOrdinarySchedule(t *testing.T) {
	s, now, source, _ := scheduledStore(t)
	ctx := context.Background()
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, inputs, source.ID)); err != nil {
		t.Fatal("recover deleted retained import")
	}
	_, bytes := func() (int, int64) {
		var n int
		var b int64
		_ = s.withJournal(ctx, func(_ *os.Root, j *journal) (bool, error) { n, b = j.ownerUsage(admin); return false, nil })
		return n, b
	}()
	if bytes != 8+resultCharge(source.Result) {
		t.Fatalf("retained quota %d", bytes)
	}
	owner := Submitter{User: "fixtureuser1", UID: "1025"}
	j, _, err := s.Submit(ctx, SubmitRequest{Key: "user", Name: "copy", Data: []byte("copy"), Submitter: owner})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Claim(ctx)
	_ = s.Finish(ctx, j.ID, Outcome{State: StateSucceeded, Result: &Result{Bytes: 4}})
	if _, _, err := s.CreateSchedule(ctx, ScheduleRequest{Key: "user", JobID: j.ID, IntervalSeconds: 3600, Consent: true}, Access{Owner: owner}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxHistory+2; i++ {
		j := submit(t, s, fmt.Sprint(i), []byte("x"))
		_, _ = s.Claim(ctx)
		_ = s.Finish(ctx, j.ID, Outcome{State: StateSucceeded, Result: &Result{Bytes: 1}})
	}
	if _, err := os.Stat(filepath.Join(s.dir, inputs, source.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("history leaked copy")
	}
	// Pin survives trimming of the imported source job.
	list, _ := s.Schedules(ctx, Access{Admin: true})
	*now = list[0].NextAt
	_ = s.Tick(ctx)
	jobs, _ := s.List(ctx)
	found := false
	for _, j := range jobs {
		if j.ScheduleID == list[0].ID {
			found = true
		}
	}
	if !found {
		t.Fatal("history removed pin")
	}
}

func TestGlobalQuotaReservesResultBeforeRetainedCopyAdmission(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	s.limits.MaxTotalBytes = resultReserve + 1
	_, _, err := s.Submit(ctx, SubmitRequest{Key: "oversized-total", Name: "copy", Data: []byte("xx"), Submitter: admin})
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("result reservation overflow admitted: %v", err)
	}
	j := submit(t, s, "exact", []byte("x"))
	_, _ = s.Claim(ctx)
	_ = s.Finish(ctx, j.ID, Outcome{State: StateSucceeded, Result: &Result{Bytes: 1}})
	// Finish releases unused JSON reservation. Pinning still charges a copy.
	s.limits.MaxTotalBytes = resultCharge(&Result{Bytes: 1}) + 1
	if _, _, err := s.CreateSchedule(ctx, ScheduleRequest{Key: "pin", JobID: j.ID, IntervalSeconds: 3600, Consent: true}, Access{Owner: admin, Admin: true}); !errors.Is(err, ErrQuota) {
		t.Fatalf("retained copy quota %v", err)
	}
}

func TestTickReadsCopyOutsideLockAndRechecksRevocation(t *testing.T) {
	s, now, _, sc := scheduledStore(t)
	*now = now.Add(time.Hour)
	entered, resume := make(chan struct{}), make(chan struct{})
	s.readScheduleCopy = func(ctx context.Context, root *os.Root, id, digest string, size int) ([]byte, error) {
		close(entered)
		<-resume
		return readCopy(root, id, digest, size)
	}
	done := make(chan error, 1)
	go func() { done <- s.Tick(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("tick never prepared copy")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err := s.SetSchedule(ctx, sc.ID, false, Access{Owner: admin})
	close(resume)
	if tickErr := <-done; tickErr != nil {
		t.Fatal(tickErr)
	}
	if err != nil {
		t.Fatalf("copy read held journal lock: %v", err)
	}
	jobs, err := s.List(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("revoked snapshot admitted: %+v %v", jobs, err)
	}
}
