package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func TestWebAPIReplyEndToEnd(t *testing.T) {
	f := newContractFixture(t, contractServer)
	// Large replies use the production operation budget, including under race
	// instrumentation; the generic fixture's three seconds is not this API's.
	f.s.Client.(*Client).Timeout = ExecutorTimeout
	policy := `{"v":1,"package":"contract","app":"app","operations":["status","delegate.create","delegate.list","delegate.revoke","webapi.call"],"webapi":{"calendar":{"purpose":"Read approved data","apis":{"SYNO.Cal.Event":{"list":[6]}}}}}`
	if err := os.WriteFile(filepath.Join(f.dir, "policy.json"), []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	grant, err := CreateWebAPIGrant(context.Background(), f.s.Client, contractAuth("alice"), "calendar", WebAPIGrantOptions{UntilRevoked: true})
	if err != nil {
		t.Fatal(err)
	}
	w := JobContext{Client: f.s.Client, DelegationID: grant.DelegationID}.WebAPI()
	for _, size := range []int{1 << 20, MaxWebAPIReply, MaxWebAPIReply + 1} {
		got, err := w.Call(context.Background(), "SYNO.Cal.Event", "list", 6, map[string]any{"reply_bytes": size})
		if size > MaxWebAPIReply {
			var refusal *api.Error
			if !errors.As(err, &refusal) || refusal.Code != CodeWebapiReplyTooLarge || !strings.Contains(refusal.Message, "20971521") || !strings.Contains(refusal.Message, "20971520") {
				t.Fatalf("oversize: %v", err)
			}
		} else if err != nil || len(got) != size || !json.Valid(got) {
			t.Fatalf("reply %d: got %d bytes: %v", size, len(got), err)
		}
	}
}
