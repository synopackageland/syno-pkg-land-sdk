package background

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestStructuredWorkPersistsResultAndPhase(t *testing.T) {
	s, dir := newStore(t)
	job := submit(t, s, "json", []byte(`{"query":"hello"}`))
	claimed, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := fastRunner(s, func(ctx context.Context, task *Task) (Result, error) {
		task.Report(Progress{Phase: "searching", Done: 2, Total: 2})
		return Result{JSON: json.RawMessage(`[{"title":"one"},{"title":"two"}]`)}, nil
	})
	r.Process(context.Background(), claimed)
	reopened := NewStore(dir)
	done, err := reopened.GetFor(context.Background(), job.ID, Access{Owner: admin})
	if err != nil || done.State != StateSucceeded || done.Progress.Phase != "searching" {
		t.Fatalf("%+v %v", done, err)
	}
	raw, err := reopened.ResultFor(context.Background(), job.ID, Access{Owner: admin})
	if err != nil || string(raw) != `[{"title":"one"},{"title":"two"}]` {
		t.Fatalf("%s %v", raw, err)
	}
	page, err := reopened.ResultPageFor(context.Background(), job.ID, Access{Owner: admin}, 1, 1)
	if err != nil || len(page.Items) != 1 || string(page.Items[0]) != `{"title":"two"}` || page.Total != 2 || page.NextOffset != nil {
		t.Fatalf("%+v %v", page, err)
	}
}

func TestStructuredResultAccessAndPaging(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "json", []byte("x"))
	if _, err := s.ResultFor(ctx, job.ID, Access{Owner: admin}); !errors.Is(err, ErrResultUnavailable) {
		t.Fatal(err)
	}
	_, _ = s.Claim(ctx)
	if err := s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{JSON: json.RawMessage(`[1,2,3]`)}}); err != nil {
		t.Fatal(err)
	}
	for _, access := range []Access{{}, {Owner: Submitter{User: admin.User, UID: "1025"}}, {Owner: Submitter{User: "bob", UID: admin.UID}}} {
		if _, err := s.ResultFor(ctx, job.ID, access); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if _, err := s.ResultPageFor(ctx, job.ID, access, -1, 0); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	page, err := s.ResultPageFor(ctx, job.ID, Access{Admin: true}, 0, 2)
	if err != nil || len(page.Items) != 2 || page.NextOffset == nil || *page.NextOffset != 2 {
		t.Fatalf("%+v %v", page, err)
	}
	for _, bounds := range [][2]int{{-1, 1}, {0, 0}, {0, MaxResultPageItems + 1}, {4, 1}} {
		if _, err := s.ResultPageFor(ctx, job.ID, Access{Admin: true}, bounds[0], bounds[1]); !errors.Is(err, ErrInvalid) {
			t.Fatal(bounds, err)
		}
	}
	page, err = s.ResultPageFor(ctx, job.ID, Access{Admin: true}, 3, 2)
	if err != nil || len(page.Items) != 0 || page.NextOffset != nil {
		t.Fatalf("%+v %v", page, err)
	}
	if _, err = s.PurgeOwner(ctx, admin, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResultFor(ctx, job.ID, Access{Admin: true}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestStructuredResultValidationAndCancelPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, raw, code string }{
		{"invalid", `{`, "result-invalid"},
		{"large", `"` + strings.Repeat("x", MaxResultBytes) + `"`, "result-too-large"},
		{"escaped", `"` + strings.Repeat("<", MaxResultBytes/2) + `"`, "result-too-large"},
	} {
		for _, cancel := range []bool{false, true} {
			t.Run(fmt.Sprint(tc.name, cancel), func(t *testing.T) {
				s, _ := newStore(t)
				ctx := context.Background()
				job := submit(t, s, "k", []byte("x"))
				claimed, _ := s.Claim(ctx)
				if cancel {
					if _, err := s.Cancel(ctx, job.ID); err != nil {
						t.Fatal(err)
					}
				}
				r := fastRunner(s, func(context.Context, *Task) (Result, error) { return Result{JSON: json.RawMessage(tc.raw)}, nil })
				r.Process(ctx, claimed)
				got, _ := s.Get(ctx, job.ID)
				state, code := StateFailed, tc.code
				if cancel {
					state, code = StateCanceled, ""
				}
				if got.State != state || got.ErrorCode != code || got.Result != nil {
					t.Fatalf("%+v", got)
				}
				next := submit(t, s, "next", []byte("y"))
				if claim, err := s.Claim(ctx); err != nil || claim == nil || claim.ID != next.ID {
					t.Fatalf("%+v %v", claim, err)
				}
			})
		}
	}
}

func TestStructuredWorkRejectsChangedInputBeforeInvocation(t *testing.T) {
	s, dir := newStore(t)
	job := submit(t, s, "k", []byte("x"))
	claimed, _ := s.Claim(context.Background())
	if err := os.WriteFile(dir+"/inputs/"+job.ID, []byte("y"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	r := fastRunner(s, func(context.Context, *Task) (Result, error) {
		called = true
		return Result{JSON: json.RawMessage(`{}`)}, nil
	})
	r.Process(context.Background(), claimed)
	got, _ := s.Get(context.Background(), job.ID)
	if called || got.ErrorCode != "input-changed" || got.State != StateFailed {
		t.Fatalf("called=%v %+v", called, got)
	}
}

func TestStructuredNullAndObjectResults(t *testing.T) {
	for _, raw := range []string{`null`, `{"count":1}`} {
		s, _ := newStore(t)
		ctx := context.Background()
		job := submit(t, s, "k", []byte("x"))
		_, _ = s.Claim(ctx)
		if err := s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{JSON: json.RawMessage(raw)}}); err != nil {
			t.Fatal(err)
		}
		got, err := s.ResultFor(ctx, job.ID, Access{Admin: true})
		if err != nil || string(got) != raw {
			t.Fatalf("%s %v", got, err)
		}
		if _, err := s.ResultPageFor(ctx, job.ID, Access{Admin: true}, 0, 1); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
}

func TestSingletonAdmissionIsAtomicAndOwnerBound(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	const count = 12
	admissions := make(chan Admission, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := NewStore(dir).SubmitSingleton(ctx, "search", SubmitRequest{Key: fmt.Sprintf("k%d", i), Name: "search", Data: []byte(fmt.Sprint(i)), Submitter: admin})
			admissions <- a
			errs <- err
		}()
	}
	wg.Wait()
	close(admissions)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	accepted, id := 0, ""
	for a := range admissions {
		if a.Outcome == "accepted" {
			accepted++
		} else if a.Outcome != "busy" {
			t.Fatalf("%+v", a)
		}
		if id == "" {
			id = a.Job.ID
		}
		if a.Job.ID != id {
			t.Fatal("duplicate singleton jobs")
		}
	}
	if accepted != 1 {
		t.Fatal(accepted)
	}
	bob := Submitter{User: "bob", UID: "1025"}
	other, err := s.SubmitSingleton(ctx, "search", SubmitRequest{Key: "b", Name: "search", Data: []byte("b"), Submitter: bob})
	if err != nil || other.Outcome != "accepted" || other.Job.ID == id {
		t.Fatalf("%+v %v", other, err)
	}
	if _, err := s.PauseFor(ctx, id, Access{Admin: true}); err != nil {
		t.Fatal(err)
	}
	busy, err := s.SubmitSingleton(ctx, "search", SubmitRequest{Key: "next", Name: "search", Data: []byte("next"), Submitter: admin})
	if err != nil || busy.Outcome != "busy" || busy.Job.ID != id {
		t.Fatalf("%+v %v", busy, err)
	}
	if _, err := s.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	next, err := s.SubmitSingleton(ctx, "search", SubmitRequest{Key: "next", Name: "search", Data: []byte("next"), Submitter: admin})
	if err != nil || next.Outcome != "accepted" || next.Job.ID == id {
		t.Fatalf("%+v %v", next, err)
	}
}

func TestStructuredResultBoundaryAndQuotaRelease(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	raw := json.RawMessage(`"` + strings.Repeat("x", MaxResultBytes-2) + `"`)
	job := submit(t, s, "large", []byte("x"))
	_, _ = s.Claim(ctx)
	if err := s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{JSON: raw}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ResultFor(ctx, job.ID, Access{Owner: admin})
	if err != nil || len(got) != MaxResultBytes {
		t.Fatalf("size %d %v", len(got), err)
	}
	stored, _ := s.Get(ctx, job.ID)
	s.limits.MaxTotalBytes = 1 + resultCharge(stored.Result) + resultReserve
	if _, _, err := s.Submit(ctx, SubmitRequest{Key: "quota", Name: "work", Data: []byte("x"), Submitter: admin}); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	if _, err := s.PurgeOwner(ctx, admin, "admin"); err != nil {
		t.Fatal(err)
	}
	submit(t, s, "quota", []byte("x"))
}

func TestStructuredResultsAreTrimmedWithHistory(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	first := ""
	for i := 0; i < MaxHistory+1; i++ {
		job := submit(t, s, fmt.Sprint(i), []byte("x"))
		if i == 0 {
			first = job.ID
		}
		_, _ = s.Claim(ctx)
		if err := s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{JSON: json.RawMessage(`{"ok":true}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	reopened := NewStore(dir)
	if _, err := reopened.ResultFor(ctx, first, Access{Admin: true}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	jobs, err := reopened.List(ctx)
	if err != nil || len(jobs) != MaxHistory {
		t.Fatalf("jobs=%d %v", len(jobs), err)
	}
}

func TestSchemaTwoMigratesWithoutLosingResults(t *testing.T) {
	s, dir := newStore(t)
	job := submit(t, s, "k", []byte("x"))
	ctx := context.Background()
	raw, err := os.ReadFile(dir + "/journal.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"schema":3`, `"schema":2`, 1))
	if err := os.WriteFile(dir+"/journal.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Claim(ctx)
	if err := s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{JSON: json.RawMessage(`true`)}}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(dir + "/journal.json")
	if !strings.Contains(string(raw), `"schema":3`) {
		t.Fatal(string(raw))
	}
	if got, err := NewStore(dir).ResultFor(ctx, job.ID, Access{Admin: true}); err != nil || string(got) != "true" {
		t.Fatalf("%s %v", got, err)
	}
}

func TestSingletonIdempotencyConflictAndTerminalReuse(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	req := SubmitRequest{Key: "one", Name: "search", Data: []byte("x"), Submitter: admin}
	a, err := s.SubmitSingleton(ctx, "search", req)
	if err != nil {
		t.Fatal(err)
	}
	req.Data = []byte("y")
	if _, err := s.SubmitSingleton(ctx, "search", req); !errors.Is(err, ErrKeyConflict) {
		t.Fatal(err)
	}
	req.Data = []byte("x")
	if _, err := s.SubmitSingleton(ctx, "test", req); !errors.Is(err, ErrKeyConflict) {
		t.Fatal(err)
	}
	if _, err := s.Cancel(ctx, a.Job.ID); err != nil {
		t.Fatal(err)
	}
	same, err := s.SubmitSingleton(ctx, "search", req)
	if err != nil || same.Outcome != "accepted" || !same.Duplicate || same.Job.ID != a.Job.ID {
		t.Fatalf("%+v %v", same, err)
	}
	for _, scope := range []string{"", "with space", strings.Repeat("a", 65)} {
		if _, err := s.SubmitSingleton(ctx, scope, req); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
}

func TestPhaseIsBoundedWithoutSplittingUTF8(t *testing.T) {
	s, _ := newStore(t)
	job := submit(t, s, "k", []byte("x"))
	ctx := context.Background()
	_, _ = s.Claim(ctx)
	got, err := s.Report(ctx, job.ID, &Progress{Phase: strings.Repeat("界", 30) + string([]byte{0xff})}, "")
	if err != nil || got.Progress.Phase != strings.Repeat("界", 21) {
		t.Fatalf("%+v %v", got.Progress, err)
	}
}
