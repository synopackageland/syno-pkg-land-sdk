//go:build linux

package dsm

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestLookupAccountBridgeErrorsAndAdmission(t *testing.T) {
	saved := accountBridges
	t.Cleanup(func() { accountBridges = saved })
	accountBridges = map[string][]byte{runtime.GOARCH: []byte("#!/bin/sh\ncase \"$1\" in missing) exit 3;; unavailable) exit 2;; slow) exec /bin/sleep 10;; *) printf '%s\\n' '" + normalAccountJSON + "';; esac\n")}
	for _, tc := range []struct {
		name string
		want error
	}{
		{"missing", ErrAccountNotFound},
		{"unavailable", ErrAccountUnavailable},
		{"slow", ErrAccountTimeout},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		account, err := LookupAccount(ctx, tc.name)
		cancel()
		if !errors.Is(err, tc.want) || account.Exists || time.Since(start) > time.Second {
			t.Fatalf("%s: %+v %v", tc.name, account, err)
		}
		if tc.want == ErrAccountTimeout && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	}
	if enabled, err := AccountEnabled(context.Background(), "alice"); err != nil || !enabled {
		t.Fatal(enabled, err)
	}
	accountBridges[runtime.GOARCH] = []byte("#!/bin/sh\nprintf '%s\\n' '" + disabledAccountJSON + "'\n")
	if enabled, err := AccountEnabled(context.Background(), "alice"); err != nil || enabled {
		t.Fatal(enabled, err)
	}
}

const disabledAccountJSON = `{"username":"alice","uid":1026,"gid":100,"home":"/home/alice","admin":true,"expired":"now","groups":[100],"source":"local"}`

func TestNativeAccountSourceAndAbsence(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Fatal("native account fixture requires cc:", err)
	}
	binary := filepath.Join(t.TempDir(), "account")
	if out, err := exec.Command(cc, "-std=c11", "-O2", "-Wall", "-Wextra", "-Werror",
		"testdata/account_fixture.c", "-ldl", "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("build native fixture: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name string
		want error
	}{
		{`DOMAIN\alice`, nil},
		{`DOMAIN\missing`, ErrAccountNotFound},
		{`DOMAIN\ioerror`, ErrAccountUnavailable},
		{`DOMAIN\offline`, ErrAccountUnavailable},
		{`DOMAIN\changed`, ErrAccountUnavailable},
		{`ldap@example.test`, ErrAccountUnavailable},
	} {
		cmd := exec.Command(binary, tc.name)
		cmd.Env = []string{"LANG=C"}
		out, runErr := cmd.Output()
		account, err := parseAccountInfo(out, tc.name, runErr)
		if !errors.Is(err, tc.want) || (tc.want == nil) != account.Exists {
			t.Fatalf("%s: %+v %v output=%s", tc.name, account, err, out)
		}
		if tc.want == nil && (account.Source != AccountSourceDomain || !account.Admin ||
			account.UID != 2969567732 || len(account.Groups) != 2) {
			t.Fatal(account)
		}
		if tc.want != nil && len(out) != 0 {
			t.Fatal("failed identity leaked a partial result")
		}
	}
}
