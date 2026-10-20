package idgateway

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLocalAccountsListsThePasswdFile(t *testing.T) {
	got, err := localAccounts(writePasswd(t, passwdFixture))
	if err != nil || !reflect.DeepEqual(got, map[int]string{0: "root", 1026: "alice", 1027: "bob"}) {
		t.Fatalf("%v %v", got, err)
	}
}

func TestLocalAccountsRefusesAPasswdFileWithoutRoot(t *testing.T) {
	// A truncated or half-written file must never look like mass deletion.
	for _, body := range []string{"", "alice:x:1026:100::/home:/sbin/nologin\n"} {
		if _, err := localAccounts(writePasswd(t, body)); err == nil {
			t.Fatalf("%q accepted", body)
		}
	}
	if _, err := localAccounts(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestSeenStoreRecordsClassifiesAndForgets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sso", "seen-uids.json")
	s := seenStore{path: path}
	if got, err := s.load(); err != nil || len(got) != 0 {
		t.Fatalf("empty store: %v %v", got, err)
	}
	local := map[int]string{0: "root", 1026: "alice"}
	if err := s.record(Subject{UID: 1026, Name: "alice"}, local); err != nil {
		t.Fatal(err)
	}
	if err := s.record(Subject{UID: 800001, Name: "CORP\\dave"}, local); err != nil {
		t.Fatal(err)
	}
	got, err := seenStore{path: path}.load()
	want := map[int]SeenAccount{1026: {Name: "alice", Local: true}, 800001: {Name: "CORP\\dave"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v %v", got, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if err := s.forget(1026); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.load(); !reflect.DeepEqual(got, map[int]SeenAccount{800001: {Name: "CORP\\dave"}}) {
		t.Fatalf("after forget: %v", got)
	}
}

func TestSeenStoreKeepsAnAccountLocalOnceSeenLocal(t *testing.T) {
	// A local account signing in again while passwd is unreadable must not
	// turn into a domain account, which is never deleted automatically.
	s := seenStore{path: filepath.Join(t.TempDir(), "seen-uids.json")}
	s.record(Subject{UID: 1026, Name: "alice"}, map[int]string{0: "root", 1026: "alice"})
	s.record(Subject{UID: 1026, Name: "alice2"}, nil)
	if got, _ := s.load(); got[1026] != (SeenAccount{Name: "alice2", Local: true}) {
		t.Fatalf("%v", got)
	}
}
