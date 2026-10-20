package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
)

func TestNativeDirectoryAccountDoesNotDependOnCoreUserExpired(t *testing.T) {
	name := `DOMAIN\alice`
	for _, tc := range []struct {
		label, expiry string
		err           error
		pass          bool
	}{
		{"native normal without Core.User expired", "normal", nil, true},
		{"native disabled", "now", nil, false},
		{"native expired", "2020/1/1", nil, false},
		{"DC unavailable", "", errors.New("unavailable"), false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			c := &cmdStub{reply: `{"success":true,"data":{"users":[{"name":"DOMAIN\\alice","uid":1200}]}}`}
			calls := 0
			d := &liveDSM{Administrator: func(context.Context, string) (bool, error) { return false, nil }, Command: c, NativeAccount: func(context.Context, string) (Subject, error) {
				calls++
				return Subject{Username: name, UID: 1200, GID: 100, Groups: []int{100}, Expired: tc.expiry}, tc.err
			}}
			_, err := d.Account(context.Background(), name)
			if (err == nil) != tc.pass || calls != 1 || c.path != "" {
				t.Fatalf("err=%v calls=%d fallback=%q", err, calls, c.path)
			}
		})
	}
}

func TestNativeAccountBindsNameAndUIDAndFailsClosed(t *testing.T) {
	for _, raw := range []string{
		`{"username":"other","uid":1200,"gid":100,"groups":[100],"expired":"normal","admin":false,"home":"/home/a"}`,
		`{"username":"alice","uid":0,"gid":100,"groups":[100],"expired":"normal","admin":false,"home":"/home/a"}`,
		`{"username":"alice","uid":1200,"gid":100,"groups":[],"expired":"normal","admin":false,"home":"/home/a"}`,
		`{"username":"alice","uid":1200,"gid":100,"groups":[100],"expired":"unknown","admin":false,"home":"/home/a"}`,
	} {
		if _, err := parseNativeAccount([]byte(raw), "alice", nil); err == nil {
			t.Fatal(raw)
		}
	}
}

func TestNativeAccountNormalOutputPreservesDirectoryUID(t *testing.T) {
	raw := []byte(`{"username":"DOMAIN\\alice","uid":2969567732,"gid":2969567745,"groups":[2969567745],"expired":"normal","admin":true,"home":"/home/alice"}`)
	s, err := parseNativeAccount(raw, `DOMAIN\alice`, nil)
	if strconv.IntSize == 32 {
		if err == nil {
			t.Fatal("oversized UID accepted")
		}
		return
	}
	if err != nil || uint64(s.UID) != 2969567732 || !s.Admin {
		t.Fatal(s, err)
	}
}

func TestNativeAccountBridgesMatchInventory(t *testing.T) {
	for arch, file := range map[string]string{"amd64": "x86_64", "arm64": "aarch64", "arm": "armv7"} {
		data, err := os.ReadFile("../dsm/native/account-" + file + ".elf")
		if err != nil || !bytes.Equal(data, accountBridges[arch]) {
			t.Fatalf("%s: embedded payload mismatch: %v", arch, err)
		}
	}
}
