package idgateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeleteActionValidation(t *testing.T) {
	good := []DeleteAction{
		{Remove: "users/{uid}"},
		{Remove: "data/{name}/cache"},
		{Exec: []string{"/var/packages/App/target/bin/app", "user", "delete", "--name={name}", "{uid}"}},
	}
	for _, a := range good {
		if err := a.validate(); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
	bad := []DeleteAction{
		{},
		{Remove: "x", Exec: []string{"/bin/true"}},
		{Remove: "/abs/{uid}"},
		{Remove: "../{uid}"},
		{Remove: "users/../../{uid}"},
		{Remove: "."},
		{Remove: "users"},
		{Remove: "sso/{uid}"},
		{Remove: "users/{home}"},
		{Exec: []string{"bin/app", "{uid}"}},
		{Exec: []string{"/bin/{name}"}},
		{Exec: []string{"/bin/app", "{password}"}},
	}
	for _, a := range bad {
		if err := a.validate(); err == nil {
			t.Errorf("%+v accepted", a)
		}
	}
}

func TestRemoveDeletesThePathUnderTheVarDir(t *testing.T) {
	varDir := t.TempDir()
	os.MkdirAll(filepath.Join(varDir, "users", "1027", "deep"), 0o700)
	os.WriteFile(filepath.Join(varDir, "users", "1027", "deep", "f"), []byte("x"), 0o600)
	os.MkdirAll(filepath.Join(varDir, "users", "1026"), 0o700)
	r := actionRunner{varDir: varDir}
	if err := r.run(context.Background(), []DeleteAction{{Remove: "users/{uid}"}}, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(varDir, "users", "1027")); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(varDir, "users", "1026")); err != nil {
		t.Fatalf("other account removed: %v", err)
	}
	// Already gone is done, so a retried deletion succeeds.
	if err := r.run(context.Background(), []DeleteAction{{Remove: "users/{uid}"}}, bob); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveNeverFollowsASymlinkOutOfTheVarDir(t *testing.T) {
	varDir, outside := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600)
	os.Symlink(outside, filepath.Join(varDir, "users"))
	r := actionRunner{varDir: varDir}
	r.run(context.Background(), []DeleteAction{{Remove: "users/keep"}}, Subject{UID: 1027, Name: "keep"})
	r.run(context.Background(), []DeleteAction{{Remove: "users/{name}"}}, Subject{UID: 1027, Name: "keep"})
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatalf("followed the symlink: %v", err)
	}
}

func TestRemoveRefusesANameThatIsAPath(t *testing.T) {
	varDir := t.TempDir()
	os.MkdirAll(filepath.Join(varDir, "users", "x"), 0o700)
	r := actionRunner{varDir: varDir}
	for _, name := range []string{"..", "a/b", "", "."} {
		if err := r.run(context.Background(), []DeleteAction{{Remove: "users/{name}"}}, Subject{UID: 1027, Name: name}); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(varDir, "users")); err != nil {
		t.Fatal(err)
	}
}

func TestExecPassesPlaceholdersAsArgvElementsOnly(t *testing.T) {
	out := filepath.Join(t.TempDir(), "argv")
	script := writeScript(t, `printf '%s\n' "$@" > "`+out+`"; printf '%s %s\n' "$SYNOPKGLAND_SSO_UID" "$SYNOPKGLAND_SSO_USER" >> "`+out+`"`)
	r := actionRunner{varDir: t.TempDir()}
	s := Subject{UID: 1027, Name: "bob; rm -rf $HOME"}
	if err := r.run(context.Background(), []DeleteAction{{Exec: []string{script, "delete", "--name={name}", "{uid}"}}}, s); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if want := "delete\n--name=bob; rm -rf $HOME\n1027\n1027 bob; rm -rf $HOME\n"; string(got) != want {
		t.Fatalf("%q", got)
	}
}

func TestExecFailuresAndTimeoutsStopTheActions(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	after := DeleteAction{Exec: []string{writeScript(t, `touch "`+marker+`"`)}}
	r := actionRunner{varDir: t.TempDir()}
	err := r.run(context.Background(), []DeleteAction{{Exec: []string{writeScript(t, `echo "no such user" >&2; exit 3`)}}, after}, bob)
	if err == nil || !strings.Contains(err.Error(), "no such user") {
		t.Fatalf("%v", err)
	}
	r.timeout = 300 * time.Millisecond
	start := time.Now()
	if err := r.run(context.Background(), []DeleteAction{{Exec: []string{writeScript(t, `exec sleep 10`)}}, after}, bob); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("ran an action after a failure")
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "action")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
