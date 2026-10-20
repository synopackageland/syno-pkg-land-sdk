package delegate

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func absentExecutor(t *testing.T) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "boot-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return &Client{Socket: filepath.Join(dir, "s")}
}

func retryJob(t *testing.T, s *Service, expires time.Time) string {
	t.Helper()
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("download")) }))
	t.Cleanup(web.Close)
	data, _ := json.Marshal(DownloadInput{URL: web.URL, Dest: "/home", Name: "file"})
	job, err := s.Store.SubmitDelegated(context.Background(), background.SubmitRequest{Key: "retry", Name: "file", Data: data, Submitter: background.Submitter{User: "alice", UID: "1205"}}, func(string) (background.DelegationBinding, error) {
		return background.DelegationBinding{DelegationID: "grant", ExpiresAt: expires}, nil
	}, func(background.DelegationBinding) { t.Error("unexpected rollback") })
	if err != nil {
		t.Fatal(err)
	}
	return job.ID
}
func retryClock(s *Service) *time.Time {
	now := time.Now()
	s.now = func() time.Time { return now }
	s.wait = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	return &now
}
func assertJob(t *testing.T, s *Service, id string, state background.State, code string) {
	t.Helper()
	job, err := s.Store.Get(context.Background(), id)
	if err != nil || job.State != state || job.ErrorCode != code {
		t.Fatalf("job=%+v err=%v want %s/%s", job, err, state, code)
	}
}
func TestExecutorAppearsAfterBootDelay(t *testing.T) {
	s := service(t, absentExecutor(t))
	now := retryClock(s)
	start := *now
	id := retryJob(t, s, now.Add(time.Hour))
	var delays []time.Duration
	writes := 0
	ready := executor(t, func(q Request) string {
		if q.Op == "files.write" {
			writes++
		}
		return `{"ok":true,"result":{}}` + "\n"
	})
	s.wait = func(ctx context.Context, d time.Duration) error {
		delays = append(delays, d)
		*now = now.Add(d)
		if now.Sub(start) >= 100*time.Second {
			// The configured socket path itself appears late; client configuration stays fixed.
			if err := os.Rename(ready.Socket, s.Client.(*Client).Socket); err != nil {
				t.Fatal(err)
			}
		}
		out := rpc(t, s, "alice", "delegate.jobs.list", "{}")
		list := out["data"].(map[string]any)["jobs"].([]any)
		if list[0].(map[string]any)["state"] != "waiting_executor" {
			t.Fatalf("not waiting: %v", out)
		}
		return ctx.Err()
	}
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertJob(t, s, id, background.StateSucceeded, "")
	if writes != 1 {
		t.Fatalf("writes=%d", writes)
	}
	want := []time.Duration{2, 4, 8, 16, 30, 30, 30}
	if len(delays) != len(want) {
		t.Fatal(delays)
	}
	for i, d := range want {
		if delays[i] != d*time.Second {
			t.Fatal(delays)
		}
	}
}
func TestExecutorWaitStopsAtFifteenMinutes(t *testing.T) {
	s := service(t, absentExecutor(t))
	now := retryClock(s)
	start := *now
	id := retryJob(t, s, now.Add(time.Hour))
	// Revocation remains pending while the executor is absent.
	_ = s.RunOnce(context.Background())
	assertJob(t, s, id, background.StateFailed, "EXECUTOR_UNAVAILABLE")
	if now.Sub(start) != 15*time.Minute {
		t.Fatalf("wait=%s", now.Sub(start))
	}
	pending, err := s.Store.DelegationCleanup(context.Background())
	if err != nil || len(pending) != 1 {
		t.Fatalf("grant cleanup lost: %v %v", pending, err)
	}
}
func TestExecutorWaitHonorsGrantExpiry(t *testing.T) {
	s := service(t, absentExecutor(t))
	now := retryClock(s)
	start := *now
	id := retryJob(t, s, now.Add(5*time.Second))
	_ = s.RunOnce(context.Background())
	assertJob(t, s, id, background.StateFailed, "DELEGATION_EXPIRED")
	if now.Sub(start) != 5*time.Second {
		t.Fatalf("expiry delayed: %s", now.Sub(start))
	}
}
func TestAcceptedWriteIsNeverRetried(t *testing.T) {
	writes := 0
	s := service(t, executor(t, func(q Request) string {
		if q.Op == "files.write" {
			writes++
			return ""
		}
		return `{"ok":true,"result":{}}` + "\n"
	}))
	now := retryClock(s)
	s.wait = func(context.Context, time.Duration) error { t.Fatal("accepted write retried"); return nil }
	id := retryJob(t, s, now.Add(time.Hour))
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertJob(t, s, id, background.StateFailed, "EXECUTOR_PROTOCOL")
	if writes != 1 {
		t.Fatal(writes)
	}
}
