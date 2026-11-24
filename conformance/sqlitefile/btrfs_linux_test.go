//go:build linux

package sqlitefile_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitefile"
	"golang.org/x/sys/unix"
)

func TestSQLiteFileBtrfsPolicies(t *testing.T) {
	const compression = "btrfs.compression"
	t.Setenv("SYNO_SQLITE_NOCOW", "")
	for _, policy := range []string{"flag", "property", "opt-out"} {
		t.Run(policy, func(t *testing.T) {
			dir := btrfsTestDir(t)
			switch policy {
			case "flag":
				f, err := os.Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				const fsComprFL = 0x00000004
				if err := unix.IoctlSetPointerInt(int(f.Fd()), unix.FS_IOC_SETFLAGS, fsComprFL); err != nil {
					t.Fatal(err)
				}
			case "property":
				if err := unix.Setxattr(dir, compression, []byte("zlib"), 0); err != nil {
					t.Fatal(err)
				}
			case "opt-out":
				t.Setenv("SYNO_SQLITE_NOCOW", "0")
			}
			if err := sqlitefile.PrepareDir(dir); err != nil {
				t.Fatal(err)
			}
			if got := sqlitefile.State(dir); got != sqlitefile.StateOff {
				t.Fatalf("directory state=%v with %s", got, policy)
			}
			path := filepath.Join(dir, "main.db")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if got := sqlitefile.State(path); got != sqlitefile.StateOff {
				t.Fatalf("new file state=%v with %s", got, policy)
			}
			if changed, err := sqlitefile.ConvertToNoCOW(context.Background(), path, nil); changed || err != nil {
				t.Fatalf("conversion with %s = %v, %v", policy, changed, err)
			}
		})
	}
}

func TestSQLiteFileBtrfsRecoveredWAL(t *testing.T) {
	path := os.Getenv("SPL_SQLITEFILE_WAL_FIXTURE")
	if path != "" {
		db := sqliteNew(t, path)
		sqliteExec(t, db, "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA wal_autocheckpoint=0",
			"CREATE TABLE entries(value)", "INSERT INTO entries VALUES ('committed through WAL')")
		// Exit without closing. SQLite leaves committed frames for recovery.
		os.Exit(0)
	}

	dir := btrfsTestDir(t)
	t.Setenv("SYNO_SQLITE_NOCOW", "")
	path = filepath.Join(dir, "main.db")
	child := exec.Command(os.Args[0], "-test.run=^TestSQLiteFileBtrfsRecoveredWAL$")
	child.Env = append(os.Environ(), "SPL_SQLITEFILE_WAL_FIXTURE="+path)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("create uncheckpointed fixture: %v\n%s", err, output)
	}
	info, err := os.Stat(path + "-wal")
	if err != nil || info.Size() == 0 {
		t.Fatalf("expected committed WAL frames: %v, %v", info, err)
	}
	if err := sqlitefile.PrepareDir(dir); err != nil {
		t.Fatal(err)
	}
	changed, err := sqlitefile.ConvertToNoCOW(context.Background(), path, sqliteOpen)
	if !changed || err != nil {
		t.Fatalf("convert with uncheckpointed WAL = %v, %v", changed, err)
	}
	db := sqliteNew(t, path)
	if got := sqliteInt(t, db, "SELECT count(*) FROM entries WHERE value='committed through WAL'"); got != 1 {
		t.Fatalf("committed WAL rows=%d", got)
	}
}

func TestSQLiteFileBtrfsBusyCheckpoint(t *testing.T) {
	dir := btrfsTestDir(t)
	t.Setenv("SYNO_SQLITE_NOCOW", "")
	path := filepath.Join(dir, "main.db")
	db := sqliteNew(t, path)
	sqliteExec(t, db, "PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE entries(value)", "INSERT INTO entries VALUES (1)")
	reader, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var value int
	if err := reader.QueryRow("SELECT value FROM entries").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err := sqlitefile.PrepareDir(dir); err != nil {
		t.Fatal(err)
	}
	changed, err := sqlitefile.ConvertToNoCOW(context.Background(), path, sqliteOpen)
	if changed || err == nil || !strings.Contains(err.Error(), "checkpoint") {
		t.Fatalf("convert during active WAL read = %v, %v", changed, err)
	}
	if got := sqlitefile.State(path); got != sqlitefile.StateOff {
		t.Fatalf("state after busy checkpoint=%v", got)
	}
}

func TestSQLiteFileBtrfsInsufficientSpace(t *testing.T) {
	dir := btrfsTestDir(t)
	t.Setenv("SYNO_SQLITE_NOCOW", "")
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		t.Fatal(err)
	}
	free := stat.Bavail * uint64(stat.Bsize)
	if free > 1<<30 {
		t.Skip("space-check fixture requires a small scratch filesystem")
	}
	path := filepath.Join(dir, "main.db")
	db := sqliteNew(t, path)
	sqliteExec(t, db, "CREATE TABLE entries(value)", "INSERT INTO entries VALUES (1)")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a database with a logical size greater than available space
	// without filling the scratch filesystem or writing the trailing bytes.
	if err := os.Truncate(path, int64(free)+1<<20); err != nil {
		t.Fatal(err)
	}
	if err := sqlitefile.PrepareDir(dir); err != nil {
		t.Fatal(err)
	}
	changed, err := sqlitefile.ConvertToNoCOW(context.Background(), path, sqliteOpen)
	if changed || !errors.Is(err, sqlitefile.ErrInsufficientSpace) {
		t.Fatalf("convert with insufficient space = %v, %v", changed, err)
	}
	if got := sqlitefile.State(path); got != sqlitefile.StateOff {
		t.Fatalf("state after space check = %v", got)
	}
	db = sqliteNew(t, path)
	if got := sqliteInt(t, db, "SELECT value FROM entries"); got != 1 {
		t.Fatalf("source value = %d", got)
	}
}

func TestSQLiteFileBtrfsPreservesExtendedMetadata(t *testing.T) {
	dir := btrfsTestDir(t)
	t.Setenv("SYNO_SQLITE_NOCOW", "")
	path := filepath.Join(dir, "main.db")
	db := sqliteNew(t, path)
	sqliteExec(t, db, "CREATE TABLE entries(value)")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(path, "user.fixture-one", []byte("first value"), 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(path, "user.fixture-two", []byte("second value"), 0); err != nil {
		t.Fatal(err)
	}
	if err := sqlitefile.PrepareDir(dir); err != nil {
		t.Fatal(err)
	}
	if changed, err := sqlitefile.ConvertToNoCOW(context.Background(), path, sqliteOpen); changed || err == nil || !strings.Contains(err.Error(), "extended attributes") {
		t.Fatalf("conversion with mismatched metadata = %v, %v", changed, err)
	}
	changed, err := sqlitefile.ConvertToNoCOW(context.Background(), path, func(p string) (*sql.DB, error) {
		if p != path {
			// Simulate security metadata inherited by the replacement.
			for name, value := range map[string]string{"user.fixture-one": "first value", "user.fixture-two": "second value"} {
				if err := unix.Setxattr(p, name, []byte(value), 0); err != nil {
					return nil, err
				}
			}
		}
		return sqliteOpen(p)
	})
	if !changed || err != nil {
		t.Fatalf("conversion with matching metadata = %v, %v", changed, err)
	}
}
