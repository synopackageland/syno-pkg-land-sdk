package dsm

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestNativeRoleProtocolNeverGuessesPrivileges(t *testing.T) {
	for _, tc := range []struct {
		out          string
		err          error
		admin, valid bool
	}{
		{"1\n", nil, true, true}, {"0\n", nil, false, true},
		{"1\n", errors.New("failed"), false, false}, {"true\n", nil, false, false},
		{"2\n", nil, false, false}, {"-1\n", nil, false, false}, {"1\n0\n", nil, false, false},
		{"1", nil, false, false}, {" 1\n", nil, false, false}, {"", nil, false, false},
	} {
		admin, err := parseNativeRole([]byte(tc.out), tc.err)
		if admin != tc.admin || (err == nil) != tc.valid {
			t.Fatalf("%q: admin=%v err=%v", tc.out, admin, err)
		}
	}
}

func TestDirectoryPrincipalsArePreservedForNativeSDK(t *testing.T) {
	for _, name := range []string{`CORP\alice`, "alice@example.com", "CORP\\\u7ba1\u7406\u54e1", "user with space@example.com", strings.Repeat("a", 256)} {
		a := newTestAuth(t, &fakeRunner{out: name + "\n"})
		a.isAdministrator = func(_ context.Context, actual string) (bool, error) {
			if actual != name {
				t.Fatalf("principal rewritten %q => %q", name, actual)
			}
			return true, nil
		}
		id, err := a.Authenticate(sessionRequest())
		if err != nil || !id.Admin || id.User != name {
			t.Fatalf("%q: %+v %v", name, id, err)
		}
	}
	for _, name := range []string{"", "alice\nadmin", "alice\x00admin", " alice", "alice ", string([]byte{0xff}), strings.Repeat("a", 1025)} {
		if validPrincipal(name) {
			t.Fatalf("accepted invalid principal %q", name)
		}
	}
}

func TestNativeOutputBufferIsBounded(t *testing.T) {
	out := &boundedOutput{limit: 16}
	if n, err := out.Write([]byte(strings.Repeat("x", 16))); n != 16 || err != nil {
		t.Fatal(n, err)
	}
	if _, err := out.Write([]byte("1")); err == nil || out.Len() != 16 {
		t.Fatal(err, out.Len())
	}
}

func TestNativeOutputCopyCannotBypassLimit(t *testing.T) {
	out := &boundedOutput{limit: 16}
	source := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 4096))}
	_, err := io.Copy(out, source)
	if err == nil || out.Len() > 16 {
		t.Fatalf("copy bypassed output limit: len=%d err=%v", out.Len(), err)
	}
}
