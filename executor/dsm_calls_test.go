package main

import (
	"context"
	"reflect"
	"testing"
)

// The App and File Station checks each ask DSM natively once, from the
// request's address, and read no AppPriv Web API.
func TestEligibleAsksDSMNativelyForAppAndFileStation(t *testing.T) {
	c := &cmdStub{reply: `{"success":true,"data":{"users":[{"name":"alice","uid":1200,"expired":"normal"}]}}`}
	n := &nativeStub{allow: true}
	d := &liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: c, AppPriv: n.check, Accounts: accountsStub{s: Subject{Username: "alice", UID: 1200, GID: 100, Groups: []int{100}}}}
	e := &Executor{DSM: d, Policy: Policy{App: "app", Entry: "app-privilege"}}
	if _, code := e.eligible(withClientAddress(context.Background(), "192.0.2.1"), "alice", 1200, true); code != "" {
		t.Fatal(code)
	}
	want := [][3]string{{"alice", "app", "192.0.2.1"}, {"alice", "SYNO.SDS.App.FileStation3.Instance", "192.0.2.1"}}
	if !reflect.DeepEqual(n.asked, want) || c.args[1] != "api=SYNO.Core.User" {
		t.Fatal(n.asked, c.args)
	}
}

// delegate.create looks the verified account up once.
func TestCreateLooksUpAccountOnce(t *testing.T) {
	e, f, _ := setup(t)
	longGrant(t, e)
	if f.accountCalls != 1 {
		t.Fatal("account lookups", f.accountCalls)
	}
}

// The live DSM asks the embedded bridge, staging it under the protected
// package directory only on a kernel without memfd_create.
func TestLiveDSMAsksTheEmbeddedBridge(t *testing.T) {
	d := newDSM("/usr/local/etc/synopkgland/p2probe")
	if d.AppPriv == nil {
		t.Fatal("no native App Privileges check")
	}
}
