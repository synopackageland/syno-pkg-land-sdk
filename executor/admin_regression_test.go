package main

import (
	"context"
	"errors"
	"testing"
)

func TestAdministratorEntryDoesNotDependOnAppPrivilegeRegistration(t *testing.T) {
	for _, entry := range []string{"admin-only", "app-privilege", ""} {
		for _, bridgeErr := range []error{nil, errors.New("unregistered application")} {
			n := &nativeStub{allow: false, err: bridgeErr}
			d := &liveDSM{AppPriv: n.check}
			ctx := withClientAddress(context.Background(), "192.0.2.1")
			if err := d.Allow(ctx, Subject{Username: "operator", Admin: true}, "SYNO.SDS.DemoApp.Instance", entry); err != nil || len(n.asked) != 0 {
				t.Fatalf("%q: administrator depended on App Privileges: %v calls=%v", entry, err, n.asked)
			}
		}
	}
}

func TestAccountVerificationFailureIsUnavailable(t *testing.T) {
	for _, cause := range []error{errNativeAccount, context.DeadlineExceeded} {
		d := &liveDSM{NativeAccount: func(context.Context, string) (Subject, error) { return Subject{}, cause }}
		e, _, _ := setup(t)
		e.DSM = d
		_, code := e.eligible(context.Background(), "operator", 1200, false)
		if code != "INTERNAL" {
			t.Fatalf("verification error became %q instead of unavailable", code)
		}
	}
}

func TestAccountUsesSharedRoleForTheVerifiedPrincipal(t *testing.T) {
	for _, name := range []string{"operator", `CORP\operator`, "operator@example.test"} {
		for _, tc := range []struct {
			admin bool
			err   error
		}{{true, nil}, {false, nil}, {true, errors.New("bridge failed")}, {false, context.DeadlineExceeded}} {
			called := ""
			d := &liveDSM{NativeAccount: func(_ context.Context, got string) (Subject, error) {
				return Subject{Username: got, UID: 1200, GID: 100, Groups: []int{100}, Expired: "normal", Admin: !tc.admin}, nil
			}, Administrator: func(_ context.Context, got string) (bool, error) { called = got; return tc.admin, tc.err }}
			s, err := d.Account(context.Background(), name)
			if called != name || (err == nil) != (tc.err == nil) || err == nil && s.Admin != tc.admin {
				t.Fatalf("%q: subject=%+v err=%v called=%q", name, s, err, called)
			}
			if tc.err != nil && !errors.Is(err, verificationUnavailable) {
				t.Fatalf("bridge failure lost unavailable: %v", err)
			}
		}
	}
}

func TestRootStatusNeverReportsVerificationFailureAsNonAdmin(t *testing.T) {
	for _, cause := range []error{errors.New("bridge failed"), context.DeadlineExceeded} {
		e, f, _ := setup(t)
		f.accountErr = verificationError(cause)
		if r := request(e, "root.jobs.list", map[string]any{}); r.Code != "INTERNAL" {
			t.Fatalf("unknown role became %+v", r)
		}
	}
}

func TestAppPrivilegeVerificationFailureIsUnavailable(t *testing.T) {
	for _, cause := range []error{errors.New("bridge failed"), context.DeadlineExceeded} {
		d := &liveDSM{AppPriv: (&nativeStub{err: cause}).check}
		e, _, _ := setup(t)
		e.DSM = d
		e.Policy.Entry = "app-privilege"
		s := Subject{Username: "operator", UID: 1200, GID: 100, Groups: []int{100}, Expired: "normal"}
		ctx := withClientAddress(context.Background(), "192.0.2.1")
		if _, code := e.privileged(ctx, s, s.Username, s.UID, false); code != "INTERNAL" {
			t.Fatalf("unknown App Privileges became %q", code)
		}
	}
}

func TestScopeSelectionVerificationFailureIsUnavailable(t *testing.T) {
	e, f := selectedExecutor(t)
	f.accountErr = verificationError(context.DeadlineExceeded)
	if r := selectScope(e, "shared:reports", 0); r.Code != "INTERNAL" {
		t.Fatalf("unknown role became %+v", r)
	}
}
