package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// TestWebAPIParamNamesEndToEnd drives the production executor's decoder,
// grant checks and synowebapi argv builder; only the process is replaced by
// one that echoes its argv. DSM APIs take account, user, username, password
// and token as ordinary parameters, so they must reach synowebapi unchanged.
func TestWebAPIParamNamesEndToEnd(t *testing.T) {
	f := newContractFixture(t, contractServer)
	policy := `{"v":1,"package":"contract","app":"app","operations":["status","delegate.create","delegate.list","delegate.revoke","webapi.call"],"webapi":{"signin":{"purpose":"Read sign-in methods","apis":{"SYNO.SecureSignIn.Method.Admin":{"get":[1]}}}}}`
	if err := os.WriteFile(filepath.Join(f.dir, "policy.json"), []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	grant, err := CreateWebAPIGrant(context.Background(), f.s.Client, contractAuth("alice"), "signin", WebAPIGrantOptions{UntilRevoked: true})
	if err != nil {
		t.Fatal(err)
	}
	w := JobContext{Client: f.s.Client, DelegationID: grant.DelegationID}.WebAPI()
	params := map[string]any{"argv_probe": true, "account": "bob", "username": "bob", "user": "bob", "password": "secret", "uid": 1026, "token": "t", "sid": "s", "SynoToken": "s", "remote_addr": "192.0.2.1"}
	got, err := w.Call(context.Background(), "SYNO.SecureSignIn.Method.Admin", "get", 1, params)
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Data struct {
			Path string   `json:"path"`
			Argv []string `json:"argv"`
		} `json:"data"`
	}
	if err = json.Unmarshal(got, &reply); err != nil {
		t.Fatal(err)
	}
	want := []string{"--exec", "api=SYNO.SecureSignIn.Method.Admin", "method=get", "version=1", "runner=alice",
		`SynoToken="s"`, `account="bob"`, "argv_probe=true", `password="secret"`, `remote_addr="192.0.2.1"`, `sid="s"`, `token="t"`, "uid=1026", `user="bob"`, `username="bob"`}
	if reply.Data.Path != "/usr/syno/bin/synowebapi" || !reflect.DeepEqual(reply.Data.Argv, want) {
		t.Fatalf("argv %s %q", reply.Data.Path, reply.Data.Argv)
	}
	for _, key := range []string{"api", "Method", "VERSION", "runner", "Runner", "outfile", "custom_event", "compound"} {
		_, err = w.Call(context.Background(), "SYNO.SecureSignIn.Method.Admin", "get", 1, map[string]any{"argv_probe": true, "account": "bob", key: "root"})
		var refusal *api.Error
		if !errors.As(err, &refusal) || refusal.Code != CodeInvalidRequest {
			t.Fatalf("%s: %v", key, err)
		}
	}
}
