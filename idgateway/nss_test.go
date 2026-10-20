package idgateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writePasswd(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const passwdFixture = "root:x:0:0::/root:/bin/ash\nalice:x:1026:100::/var/services/homes/alice:/sbin/nologin\nbob:x:1027:100::/var/services/homes/bob:/sbin/nologin\nbroken line\n"

func TestDirectoryResolvesLocalAccountsByNameAndUID(t *testing.T) {
	d := &Directory{Passwd: writePasswd(t, passwdFixture)}
	if uid, err := d.UID(context.Background(), "alice"); err != nil || uid != 1026 {
		t.Fatalf("UID(alice) = %d, %v", uid, err)
	}
	if name, err := d.Name(context.Background(), 1027); err != nil || name != "bob" {
		t.Fatalf("Name(1027) = %q, %v", name, err)
	}
}

func TestDirectoryRefusesUnknownAccounts(t *testing.T) {
	d := &Directory{Passwd: writePasswd(t, passwdFixture)}
	if _, err := d.UID(context.Background(), "carol"); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("carol: %v", err)
	}
	if _, err := d.Name(context.Background(), 4242); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("4242: %v", err)
	}
}

func TestDirectorySeesDeletionsAndRenames(t *testing.T) {
	path := writePasswd(t, passwdFixture)
	d := &Directory{Passwd: path}
	if _, err := d.UID(context.Background(), "bob"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("alice2:x:1026:100::/h:/sbin/nologin\n"), 0o644)
	if _, err := d.UID(context.Background(), "bob"); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("deleted bob: %v", err)
	}
	if name, _ := d.Name(context.Background(), 1026); name != "alice2" {
		t.Fatalf("renamed 1026: %q", name)
	}
}

func TestDirectoryAsksNSSForAccountsOutsidePasswd(t *testing.T) {
	var asked []string
	d := &Directory{Passwd: writePasswd(t, passwdFixture), lookup: func(_ context.Context, args ...string) (string, error) {
		asked = append(asked, args...)
		if args[0] == "-u" && args[1] == `CORP\carol` {
			return "200001", nil
		}
		if args[0] == "-nu" && args[1] == "200001" {
			return `CORP\carol`, nil
		}
		return "", errors.New("no such user")
	}}
	if uid, err := d.UID(context.Background(), `CORP\carol`); err != nil || uid != 200001 {
		t.Fatalf("UID = %d, %v", uid, err)
	}
	if name, err := d.Name(context.Background(), 200001); err != nil || name != `CORP\carol` {
		t.Fatalf("Name = %q, %v", name, err)
	}
	if _, err := d.UID(context.Background(), "-x"); !errors.Is(err, ErrNoAccount) || len(asked) != 4 {
		t.Fatalf("option-like name reached id: %v %q", err, asked)
	}
}

func TestDirectoryRefusesRootAndSystemAccounts(t *testing.T) {
	d := &Directory{Passwd: writePasswd(t, passwdFixture)}
	if _, err := d.UID(context.Background(), "root"); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("root: %v", err)
	}
}
