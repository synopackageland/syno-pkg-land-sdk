package dsm

import (
	"context"
	"errors"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func TestMediaAdmissionNeverUsesCachedSessionOrEntry(t *testing.T) {
	runner := &fakeRunner{out: "person"}
	session := newTestAuth(t, runner)
	allowed := true
	checks := 0
	gate := &EntryGate{next: session, mode: EntryAppPrivilege, app: "SYNO.SDS.Demo.Instance", allowed: func(context.Context, string, string, string) (bool, error) { checks++; return allowed, nil }}
	account := AccountInfo{Username: "person", UID: 1201, Expired: "normal"}
	auth := &mediaAuthenticator{gate: gate, lookup: func(context.Context, string) (AccountInfo, error) { return account, nil }}
	if _, err := auth.Authenticate(sessionRequest()); err != nil {
		t.Fatal(err)
	}
	allowed = false
	if _, err := auth.Authenticate(sessionRequest()); !errors.Is(err, api.ErrAppPrivilegeDenied) {
		t.Fatal("cached entry survived revocation", err)
	}
	if checks != 2 {
		t.Fatal("entry not freshly checked", checks)
	}
	allowed = true
	account.Expired = "now"
	if _, err := auth.Authenticate(sessionRequest()); !errors.Is(err, api.ErrNoSession) {
		t.Fatal("disabled account admitted", err)
	}
	account.Expired = "normal"
	runner.out = ""
	if _, err := auth.Authenticate(sessionRequest()); !errors.Is(err, api.ErrNoSession) {
		t.Fatal("logged-out session admitted", err)
	}
	r := sessionRequest()
	r.Header.Del("X-SYNO-TOKEN")
	if _, err := auth.Authenticate(r); err == nil {
		t.Fatal("tokenless request reached native auth")
	}
}
func TestMediaAuthenticatorRequiresExplicitEntry(t *testing.T) {
	for _, mode := range []string{"", "invalid"} {
		if _, err := NewMediaAuthenticator(mode, "Demo"); err == nil {
			t.Fatal("invalid entry accepted")
		}
	}
	a, err := NewMediaAuthenticator(EntryAdminOnly, "Demo")
	if err != nil || a.EntryMode() != EntryAdminOnly {
		t.Fatal(err)
	}
}
