package delegatetest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/delegate"
	"github.com/synopackageland/syno-pkg-land-sdk/delegate/delegatetest"
)

func TestTheFakeExecutorAnswersAndKeepsTheRequests(t *testing.T) {
	e := delegatetest.NewExecutor(t, func(q delegate.Request) string {
		if q.Op == "root.jobs.list" {
			return delegatetest.Refused("EXECUTOR_PAUSED")
		}
		return delegatetest.OK(`{"request_id":"r1","exit_code":0}`)
	})
	c := &delegate.Client{Socket: e.Socket}
	var result struct {
		RequestID string `json:"request_id"`
	}
	if err := c.Call(context.Background(), "root.run", map[string]string{"dsm_cookie": "sid"}, map[string]string{"name": "cli"}, &result); err != nil || result.RequestID != "r1" {
		t.Fatalf("root.run: %v %+v", err, result)
	}
	var refusal *api.Error
	if err := c.Call(context.Background(), "root.jobs.list", nil, nil, nil); !errors.As(err, &refusal) || refusal.Code != "EXECUTOR_PAUSED" {
		t.Fatalf("root.jobs.list: %v", err)
	}
	seen := e.Requests()
	if len(seen) != 2 || seen[0].Op != "root.run" || seen[0].Auth["dsm_cookie"] != "sid" || string(seen[0].Params) != `{"name":"cli"}` || seen[1].Op != "root.jobs.list" {
		t.Fatalf("requests %+v", seen)
	}
}

// A Unix socket path holds at most 104 bytes on macOS; t.TempDir's often
// does not fit.
func TestTheSocketPathFitsEveryUnix(t *testing.T) {
	if e := delegatetest.NewExecutor(t, nil); len(e.Socket) >= 104 {
		t.Fatalf("socket %q", e.Socket)
	}
}
