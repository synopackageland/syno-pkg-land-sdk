package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestLongDestinationPermissionRecheckRequiresCurrentOwnerAndGrant(t *testing.T) {
	e, f, _ := setup(t)
	e.Policy.Destinations, e.Policy.Lifetime = []string{"save"}, "until-revoked"
	r := request(e, "delegate.create", map[string]any{"ops": []string{"files.moveFromStaging"}, "scope": "home", "path": "/home", "destination": "save", "lifetime": "until-revoked"})
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	var grant Delegation
	json.Unmarshal(b, &grant)
	// Production destination-only policies need no broader file permission.
	e.Policy.Operations = []string{"files.moveFromStaging", "delegate.list", "delegate.revoke", "status"}
	p := map[string]any{"delegation_id": grant.ID, "path": "/home"}
	params, _ := json.Marshal(p)
	if r := e.Handle(context.Background(), Request{Version: 1, Op: "files.checkPermission", Params: params}); r.OK {
		t.Fatal("sessionless destination check accepted", r)
	}
	if r := request(e, "files.checkPermission", p); !r.OK {
		t.Fatal(r)
	}
	if f.lastOp != "files.checkPermission" || f.lastArgs["path"] != "/home" {
		t.Fatal("no current ACL check")
	}
	f.permissionErr = errors.New("ACL removed")
	if r := request(e, "files.checkPermission", p); r.Code != "PERMISSION_DENIED" {
		t.Fatal(r)
	}
	f.permissionErr = nil
	p["path"] = "/home/child"
	if r := request(e, "files.checkPermission", p); r.OK {
		t.Fatal("grant widened", r)
	}
	p["path"] = "/home"
	uid := f.user.UID
	f.user.UID++
	if r := request(e, "files.checkPermission", p); r.OK {
		t.Fatal("reused user accepted", r)
	}
	f.user.UID = uid
	f.privilegeErr = errors.New("app denied")
	if r := request(e, "files.checkPermission", p); r.OK {
		t.Fatal("App privilege ignored", r)
	}
	f.privilegeErr = nil
	if err := e.Store.SetDisabled(true); err != nil {
		t.Fatal(err)
	}
	if r := request(e, "files.checkPermission", p); r.OK {
		t.Fatal("disabled accepted", r)
	}
	if err := e.Store.SetDisabled(false); err != nil {
		t.Fatal(err)
	}
	e.commitMu.Lock()
	grant.Revoked = true
	e.Store.Put(grant)
	e.commitMu.Unlock()
	if r := request(e, "files.checkPermission", p); r.OK {
		t.Fatal("revoked accepted", r)
	}
}
