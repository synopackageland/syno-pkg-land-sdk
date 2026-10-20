package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDSMVerifyNativeRefusesInvalidSessionsAndPrincipals(t *testing.T) {
	for label, tc := range map[string]struct {
		out  string
		err  error
		want string
	}{
		"local":        {out: "alice\n", want: "alice"},
		"AD":           {out: `CORP\alice`, want: `CORP\alice`},
		"LDAP":         {out: "alice@example.com\r\n", want: "alice@example.com"},
		"unicode":      {out: "測試\n", want: "測試"},
		"expired":      {err: errors.New("exit status 1")},
		"empty":        {out: "\n"},
		"multiline":    {out: "alice\nroot\n"},
		"html":         {out: "<html>login</html>"},
		"space":        {out: " alice\n"},
		"control":      {out: "alice\x00root"},
		"invalid UTF8": {out: "alice\xff"},
		"overlong":     {out: strings.Repeat("a", 1025)},
		"output limit": {out: strings.Repeat("a", 1027)},
	} {
		t.Run(label, func(t *testing.T) {
			d := &liveDSM{session: func(context.Context, string, []string) ([]byte, error) { return []byte(tc.out), tc.err }}
			got, err := d.Verify(withClientAddress(context.Background(), "192.0.2.7"), "session", "token")
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("user=%q error=%v", got, err)
			}
		})
	}
}

func TestDSMVerifyMissingCarriersNeverInvokesNativeAuthenticator(t *testing.T) {
	for label, tc := range map[string]struct{ addr, cookie, token string }{
		"no address":       {cookie: "session", token: "token"},
		"bad address":      {addr: "spoofed", cookie: "session", token: "token"},
		"IPv6 zone":        {addr: "fe80::1%eth0", cookie: "session", token: "token"},
		"no cookie":        {addr: "192.0.2.7", token: "token"},
		"no token":         {addr: "192.0.2.7", cookie: "session"},
		"cookie injection": {addr: "192.0.2.7", cookie: "session; id=other", token: "token"},
		"cookie control":   {addr: "192.0.2.7", cookie: "session\x7f", token: "token"},
		"cookie oversize":  {addr: "192.0.2.7", cookie: strings.Repeat("s", 4097), token: "token"},
		"token injection":  {addr: "192.0.2.7", cookie: "session", token: "tok\r\nInjected:1"},
		"token oversize":   {addr: "192.0.2.7", cookie: "session", token: strings.Repeat("t", 4097)},
	} {
		t.Run(label, func(t *testing.T) {
			d := &liveDSM{session: func(context.Context, string, []string) ([]byte, error) {
				t.Error("invalid carriers reached DSM")
				return []byte("alice"), nil
			}}
			if _, err := d.Verify(withClientAddress(context.Background(), tc.addr), tc.cookie, tc.token); err == nil {
				t.Fatal("invalid session accepted")
			}
		})
	}
}

func TestDSMVerifyNativeKeepsCallerDeadlineAndClearsInheritedEnvironment(t *testing.T) {
	t.Setenv("REMOTE_USER", "forged-admin")
	t.Setenv("HTTP_COOKIE", "id=inherited")
	t.Setenv("HTTP_X_SYNO_TOKEN", "inherited")
	dir := t.TempDir()
	path := filepath.Join(dir, "authenticate.cgi")
	// The token and cookie go only in the child's explicit environment, never argv.
	script := "#!/bin/sh\n[ $# = 0 ] && [ -z \"$REMOTE_USER\" ] && [ \"$HTTP_COOKIE\" = 'id=session' ] && [ \"$HTTP_X_SYNO_TOKEN\" = token ] && [ \"$QUERY_STRING\" = SynoToken=token ] && [ \"$REMOTE_ADDR\" = 192.0.2.7 ] && printf 'alice\\n'\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(withClientAddress(context.Background(), "192.0.2.7"), time.Second)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()
	d := &liveDSM{session: func(child context.Context, _ string, env []string) ([]byte, error) {
		deadline, ok := child.Deadline()
		if !ok || !deadline.Equal(wantDeadline) {
			t.Fatal("caller deadline lost")
		}
		return runSessionCGI(child, path, env)
	}}
	if got, err := d.Verify(ctx, "session", "token"); err != nil || got != "alice" {
		t.Fatalf("user=%q err=%v", got, err)
	}
	cancel()
	if _, err := d.Verify(ctx, "session", "token"); err == nil {
		t.Fatal("canceled authentication accepted")
	}
}

func TestNativeSessionOutputIsBounded(t *testing.T) {
	out := &sessionOutput{}
	if _, err := out.Write(make([]byte, 1026)); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("x")); err == nil {
		t.Fatal("unbounded native output")
	}
}

// These are DSM policies, not SDK switches: the native authenticator must
// receive the same browser carriers under all four security configurations.
func TestNativeSessionSecuritySettingsMatrix(t *testing.T) {
	for _, csrf := range []bool{false, true} {
		for _, skipIP := range []bool{false, true} {
			t.Run(fmt.Sprintf("csrf=%t/skip_ip_checking=%t", csrf, skipIP), func(t *testing.T) {
				for _, tc := range []struct {
					name, addr, token string
					want              bool
				}{
					{"current browser", "192.0.2.7", "token", true},
					{"different address", "127.0.0.1", "token", skipIP},
					{"wrong token", "192.0.2.7", "wrong", !csrf},
				} {
					t.Run(tc.name, func(t *testing.T) {
						d := &liveDSM{session: func(_ context.Context, _ string, env []string) ([]byte, error) {
							carriers := map[string]string{}
							for _, entry := range env {
								key, value, _ := strings.Cut(entry, "=")
								carriers[key] = value
							}
							if carriers["HTTP_COOKIE"] != "id=session" || (!skipIP && carriers["REMOTE_ADDR"] != "192.0.2.7") || (csrf && carriers["HTTP_X_SYNO_TOKEN"] != "token") {
								return nil, errors.New("DSM refused")
							}
							return []byte("alice\n"), nil
						}}
						user, err := d.Verify(withClientAddress(context.Background(), tc.addr), "session", tc.token)
						if (err == nil && user == "alice") != tc.want {
							t.Fatalf("user=%q err=%v want accepted=%t", user, err, tc.want)
						}
					})
				}
			})
		}
	}
}
