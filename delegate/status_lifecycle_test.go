package delegate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// a stopped or starting package is not an executor that waits for
// approval. delegate.status passes the lifecycle code on as a refusal, which
// the browser repeats on its own, instead of a blocked state the SDK reads as
// "needs approval".
func TestStatusPassesOnAStoppedOrStartingPackage(t *testing.T) {
	cases := map[string]ExecutorClient{
		CodeAppStopped:  &Client{Socket: filepath.Join(t.TempDir(), "absent"), Mailbox: mailbox(t, "stop", time.Hour)},
		CodeAppStarting: fakeExecutor(func(context.Context, string, map[string]string, any, any) error { return fail(CodeAppStarting) }),
		CodeExecutorStarting: fakeExecutor(func(_ context.Context, _ string, _ map[string]string, _ any, out any) error {
			return json.Unmarshal([]byte(`{"executor_digest":"e","policy_digest":"p","version":"1","ready":false,"state":"starting"}`), out)
		}),
	}
	for code, client := range cases {
		s := service(t, nil)
		s.Client = client
		out := rpc(t, s, "alice", "delegate.status", "{}")
		if e, _ := out["error"].(map[string]any); out["success"] != false || e["code"] != code {
			t.Errorf("%s: %v", code, out)
		}
		if got := s.Status(context.Background(), nil); got.State != "blocked" || got.Reason != code {
			t.Errorf("%s: Status %+v", code, got)
		}
		s.Lifetime, s.Destinations = "until-revoked", []string{"save"}
		_, err := s.Destination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "save")
		if a, ok := err.(*api.Error); !ok || a.Code != code {
			t.Errorf("%s: Destination %v", code, err)
		}
	}
}
