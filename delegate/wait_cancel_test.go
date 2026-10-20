package delegate

import (
	"context"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitingCancellationRevocationAndOtherOwnerAccess(t *testing.T) {
	for _, action := range []string{"cancel", "revoke", "parent-stop"} {
		t.Run(action, func(t *testing.T) {
			s := service(t, absentExecutor(t))
			id := retryJob(t, s, time.Now().Add(time.Hour))
			bob := background.Submitter{User: "bob", UID: "1206"}
			other, _, err := s.Store.Submit(context.Background(), background.SubmitRequest{Key: "other", Name: "other", Data: []byte("other"), Submitter: bob})
			if err != nil {
				t.Fatal(err)
			}
			cgi := &Service{Client: s.Client, Store: background.NewRequestStore(filepath.Join(filepath.Dir(s.StagingDir), "background")), LookupUID: func(user string) string {
				if user == "bob" {
					return "1206"
				}
				return "1205"
			}}
			waiting := make(chan struct{})
			s.wait = func(ctx context.Context, _ time.Duration) error { close(waiting); <-ctx.Done(); return ctx.Err() }
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			done := make(chan error, 1)
			go func() { done <- s.RunOnce(ctx) }()
			select {
			case <-waiting:
			case <-time.After(3 * time.Second):
				t.Fatal("worker did not wait")
			}
			// A separate CGI store/process shares the same lock file with the worker.
			accessDone := make(chan error, 1)
			go func() {
				list, e := cgi.Store.ListFor(context.Background(), background.Access{Owner: bob})
				if e != nil {
					accessDone <- e
					return
				}
				if len(list) != 1 || list[0].ID != other.ID {
					accessDone <- errors.New("owner list leaked waiting job")
					return
				}
				_, e = cgi.Store.CancelFor(context.Background(), other.ID, background.Access{Owner: bob})
				accessDone <- e
			}()
			select {
			case e := <-accessDone:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				t.Fatal("executor timer holds journal lock")
			}
			denied := rpc(t, cgi, "bob", "delegate.jobs.cancel", `{"id":"`+id+`"}`)
			if denied["success"] != false {
				t.Fatal("other owner canceled waiting job", denied)
			}
			switch action {
			case "cancel":
				out := rpc(t, cgi, "alice", "delegate.jobs.cancel", `{"id":"`+id+`"}`)
				if out["success"] != true || out["data"].(map[string]any)["revocation_pending"] != true {
					t.Fatal(out)
				}
			case "revoke":
				out := rpc(t, cgi, "alice", "delegate.revoke", `{"delegation_id":"grant"}`)
				if out["success"] != false {
					t.Fatal("absent executor falsely acknowledged revocation", out)
				}
			case "parent-stop":
				stop()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("waiting worker ignored cancellation")
			}
			if action == "parent-stop" {
				assertJob(t, s, id, background.StateInterrupted, "worker-stopped")
			} else {
				assertJob(t, s, id, background.StateCanceled, "")
			}
			pending, e := s.Store.DelegationCleanup(context.Background())
			if e != nil || len(pending) != 1 || pending[0].JobID != id {
				t.Fatalf("cleanup not durable: %v %v", pending, e)
			}
			entries, e := os.ReadDir(s.StagingDir)
			if e != nil || len(entries) != 0 {
				t.Fatalf("cancellation leaked staging: %v %v", entries, e)
			}
		})
	}
}
