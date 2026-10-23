package idgateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var dave = Subject{UID: 800001, Name: "CORP\\dave"}

type deletionsFixture struct {
	*Deletions
	passwd  string
	log     string
	revoked []int
	deleted []PendingDeletion
}

// newDeletions has alice and bob in passwd and dave in the domain; its
// action logs "<uid> <name>" lines, failing while a "fail" file exists.
func newDeletions(t *testing.T) *deletionsFixture {
	t.Helper()
	dir := t.TempDir()
	f := &deletionsFixture{passwd: filepath.Join(dir, "passwd"), log: filepath.Join(dir, "log")}
	os.WriteFile(f.passwd, []byte(passwdFixture), 0o644)
	action := writeScript(t, `[ -e "`+filepath.Join(dir, "fail")+`" ] && { echo "app is down" >&2; exit 1; }; echo "$1 $2" >> "`+f.log+`"`)
	f.Deletions = &Deletions{
		StateDir: filepath.Join(dir, "sso"), Passwd: f.passwd, VarDir: dir,
		Names:   fakeNames{1026: "alice", 1027: "bob", 800001: "CORP\\dave"},
		Actions: []DeleteAction{{Exec: []string{action, "{uid}", "{name}"}}},
		Revoke:  func(uid int) { f.revoked = append(f.revoked, uid) },
		Deleted: func(p PendingDeletion) { f.deleted = append(f.deleted, p) },
		Now:     func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	for _, s := range []Subject{alice, bob, dave} {
		if err := f.SignedIn(s); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *deletionsFixture) deleteLocal(t *testing.T, name string) {
	t.Helper()
	var keep []string
	for _, line := range strings.Split(passwdFixture, "\n") {
		if !strings.HasPrefix(line, name+":") {
			keep = append(keep, line)
		}
	}
	// DSM replaces passwd by rename, like this.
	tmp := f.passwd + ".new"
	os.WriteFile(tmp, []byte(strings.Join(keep, "\n")), 0o644)
	os.Rename(tmp, f.passwd)
	delete(f.Names.(fakeNames), map[string]int{"alice": 1026, "bob": 1027}[name])
}

func (f *deletionsFixture) actionLog() string {
	raw, _ := os.ReadFile(f.log)
	return string(raw)
}

func TestReconcileDeletesALocalAccountGoneFromPasswd(t *testing.T) {
	f := newDeletions(t)
	if err := f.Reconcile(context.Background()); err != nil || f.actionLog() != "" || f.revoked != nil {
		t.Fatalf("nothing deleted yet: %v %q %v", err, f.actionLog(), f.revoked)
	}
	f.deleteLocal(t, "bob")
	delete(f.Names.(fakeNames), dave.UID) // a domain account NSS cannot find
	if err := f.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.actionLog() != "1027 bob\n" || !reflect.DeepEqual(f.revoked, []int{1027}) {
		t.Fatalf("%q %v", f.actionLog(), f.revoked)
	}
	seen, _ := f.seen().load()
	if _, ok := seen[1027]; ok || len(seen) != 2 {
		t.Fatalf("seen %v", seen)
	}
	if err := f.Reconcile(context.Background()); err != nil || f.actionLog() != "1027 bob\n" {
		t.Fatalf("deleted twice: %v %q", err, f.actionLog())
	}
}

func TestReconcileNeverDeletesOnAnIncompletePasswd(t *testing.T) {
	f := newDeletions(t)
	os.WriteFile(f.passwd, []byte("alice:x:1026:100::/home:/sbin/nologin\n"), 0o644)
	if err := f.Reconcile(context.Background()); err == nil || f.actionLog() != "" || f.revoked != nil {
		t.Fatalf("%v %q %v", err, f.actionLog(), f.revoked)
	}
}

func TestAFailedDeletionStaysPendingAndIsRetried(t *testing.T) {
	f := newDeletions(t)
	fail := filepath.Join(filepath.Dir(f.passwd), "fail")
	os.WriteFile(fail, nil, 0o600)
	f.deleteLocal(t, "bob")
	f.Reconcile(context.Background())
	f.Reconcile(context.Background())
	st, err := f.Status(context.Background())
	want := []PendingDeletion{{UID: 1027, Name: "bob", Since: f.Now(), Attempts: 2}}
	if err != nil || len(st.Pending) != 1 || !strings.Contains(st.Pending[0].Error, "app is down") {
		t.Fatalf("%+v %v", st, err)
	}
	st.Pending[0].Error = ""
	if !reflect.DeepEqual(st.Pending, want) {
		t.Fatalf("%+v", st.Pending)
	}
	if f.deleted != nil {
		t.Fatalf("reported before it succeeded: %+v", f.deleted)
	}
	os.Remove(fail)
	if err := f.Reconcile(context.Background()); err != nil || f.actionLog() != "1027 bob\n" {
		t.Fatalf("%v %q", err, f.actionLog())
	}
	if len(f.deleted) != 1 || f.deleted[0].UID != 1027 || f.deleted[0].Attempts != 2 {
		t.Fatalf("completed deletion not reported: %+v", f.deleted)
	}
	if st, _ := f.Status(context.Background()); len(st.Pending) != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestDomainAccountsAreListedThenPurgedByTheAdministrator(t *testing.T) {
	f := newDeletions(t)
	delete(f.Names.(fakeNames), dave.UID)
	os.WriteFile(f.passwd, []byte(passwdFixture), 0o644)
	if st, err := f.Status(context.Background()); err != nil || !reflect.DeepEqual(st.Missing, []SeenUID{{UID: dave.UID, Name: dave.Name}}) || len(st.Pending) != 0 {
		t.Fatalf("%+v %v", st, err)
	}
	if err := f.Purge(context.Background(), alice.UID); err == nil {
		t.Fatal("purged an account DSM still has")
	}
	if err := f.Purge(context.Background(), 4242); err == nil {
		t.Fatal("purged an account that never signed in")
	}
	if f.actionLog() != "" {
		t.Fatalf("%q", f.actionLog())
	}
	if err := f.Purge(context.Background(), dave.UID); err != nil || f.actionLog() != "800001 CORP\\dave\n" {
		t.Fatalf("%v %q", err, f.actionLog())
	}
	if st, _ := f.Status(context.Background()); len(st.Missing) != 0 || len(st.Pending) != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestStatusReportsAnUnreadableStateDirectory(t *testing.T) {
	f := newDeletions(t)
	os.WriteFile(filepath.Join(f.StateDir, "seen-uids.json"), []byte("{"), 0o600)
	if _, err := f.Status(context.Background()); err == nil {
		t.Fatal("corrupt store accepted")
	}
	if err := f.Reconcile(context.Background()); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%v", err)
	}
}

// No account changes or pending actions: neither polling nor repeated verified
// sign-ins for the same identity may rewrite durable account state.
func TestIdleAccountReconciliationKeepsPersistentStateUnchanged(t *testing.T) {
	f := newDeletions(t)
	ctx := context.Background()
	if err := f.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]any {
		out := map[string]any{}
		err := filepath.Walk(f.StateDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			out[path] = []any{info.ModTime(), info.Size(), info.Mode()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	for range 12 {
		if err := f.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.SignedIn(alice); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Status(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("idle account state mutated: %v %v", before, after)
	}
}
