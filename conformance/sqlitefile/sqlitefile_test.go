package sqlitefile_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitefile"
	_ "modernc.org/sqlite"
)

func sqliteOpen(path string) (*sql.DB, error) {
	dsn := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw"}
	return sql.Open("sqlite", dsn.String())
}

func sqliteNew(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func sqliteExec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func sqliteInt(t *testing.T, db *sql.DB, stmt string) int64 {
	t.Helper()
	var value int64
	if err := db.QueryRow(stmt).Scan(&value); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
	return value
}

func checkJournalMode(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	var mode string
	if err := db.QueryRow("PRAGMA main.journal_mode").Scan(&mode); err != nil || mode != want {
		t.Fatalf("journal mode = %q, %v; want %q", mode, err, want)
	}
}

func TestSQLiteFileMaintenance(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"NONE", "INCREMENTAL"} {
		t.Run(mode, func(t *testing.T) {
			db := sqliteNew(t, filepath.Join(t.TempDir(), "main.db"))
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := sqlitefile.Configure(ctx, conn); err != nil {
				t.Fatal(err)
			}
			for pragma, want := range map[string]int64{
				"PRAGMA journal_size_limit": 64 * 1024 * 1024,
				"PRAGMA wal_autocheckpoint": 1000,
			} {
				var got int64
				if err := conn.QueryRowContext(ctx, pragma).Scan(&got); err != nil || got != want {
					t.Fatalf("%s = %d, %v; want %d", pragma, got, err, want)
				}
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			sqliteExec(t, db, "PRAGMA page_size=1024", "PRAGMA auto_vacuum="+mode,
				"CREATE TABLE entries(id INTEGER PRIMARY KEY, data BLOB)",
				"WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<1500) INSERT INTO entries SELECT n, zeroblob(1024) FROM seq",
				"DELETE FROM entries WHERE id > 1490")
			before := sqliteInt(t, db, "PRAGMA freelist_count")
			if err := sqlitefile.Maintain(ctx, db); err != nil {
				t.Fatal(err)
			}
			if got := sqliteInt(t, db, "PRAGMA freelist_count"); got != before {
				t.Fatalf("small freelist changed from %d to %d", before, got)
			}
			sqliteExec(t, db, "DELETE FROM entries WHERE id > 100")
			before = sqliteInt(t, db, "PRAGMA freelist_count")
			if before <= 1024 {
				t.Fatalf("test database has only %d free pages", before)
			}
			if err := sqlitefile.Maintain(ctx, db); err != nil {
				t.Fatal(err)
			}
			if got := sqliteInt(t, db, "PRAGMA freelist_count"); (mode == "INCREMENTAL" && got != before-1024) || (mode == "NONE" && got != before) {
				t.Fatalf("freelist changed from %d to %d in %s mode", before, got, mode)
			}
			if got := sqliteInt(t, db, "SELECT count(*) FROM entries"); got != 100 {
				t.Fatalf("kept %d rows", got)
			}
		})
	}
}

// Run against a fresh CoW directory on a scratch Btrfs mount.
func TestSQLiteFileBtrfsConversion(t *testing.T) {
	dir := btrfsTestDir(t)
	t.Setenv("SYNO_SQLITE_NOCOW", "")
	ctx := context.Background()
	path := filepath.Join(dir, "main ' ? #.sqlite")
	db := sqliteNew(t, path)
	sqliteExec(t, db, "PRAGMA journal_mode=WAL", "PRAGMA user_version=20",
		"PRAGMA application_id=42", "CREATE TABLE entries(id INTEGER PRIMARY KEY, value TEXT)",
		"INSERT INTO entries VALUES (1, 'retained')")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if err := sqlitefile.PrepareDir(dir); err != nil {
		t.Fatal(err)
	}
	if sqlitefile.State(dir) != sqlitefile.StateOn || sqlitefile.State(path) != sqlitefile.StateOff {
		t.Fatalf("start states: dir=%v db=%v", sqlitefile.State(dir), sqlitefile.State(path))
	}

	// Simulate a previous interruption during the copy.
	staging := filepath.Join(dir, fmt.Sprintf(".sqlitefile-%x", sha256.Sum256([]byte(filepath.Base(path)))))
	if err := os.WriteFile(staging, []byte("partial output"), 0600); err != nil {
		t.Fatal(err)
	}

	// Interrupt the verifier. The old DB and its inode must stay available.
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	reject := errors.New("verification denied")
	changed, err := sqlitefile.ConvertToNoCOW(ctx, path, func(p string) (*sql.DB, error) {
		if p != path {
			return nil, reject
		}
		return sqliteOpen(p)
	})
	if changed || !errors.Is(err, reject) {
		t.Fatalf("failed conversion = %v, %v", changed, err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("source inode lost: %v", err)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging path remains after failure: %v", err)
	}

	changed, err = sqlitefile.ConvertToNoCOW(ctx, path, sqliteOpen)
	if err != nil || !changed {
		t.Fatalf("conversion = %v, %v", changed, err)
	}
	after, err = os.Stat(path)
	if err != nil || os.SameFile(before, after) || after.Mode().Perm() != 0640 || sqlitefile.State(path) != sqlitefile.StateOn {
		t.Fatalf("unexpected replacement: %v, %v, state=%v", after, err, sqlitefile.State(path))
	}
	changed, err = sqlitefile.ConvertToNoCOW(ctx, path, nil)
	if err != nil || changed {
		t.Fatalf("second conversion = %v, %v", changed, err)
	}

	db = sqliteNew(t, path)
	checkJournalMode(t, db, "wal")
	if got := sqliteInt(t, db, "PRAGMA user_version"); got != 20 {
		t.Fatalf("user_version=%d", got)
	}
	if got := sqliteInt(t, db, "PRAGMA application_id"); got != 42 {
		t.Fatalf("application_id=%d", got)
	}
	if got := sqliteInt(t, db, "SELECT count(*) FROM entries WHERE id=1 AND value='retained'"); got != 1 {
		t.Fatalf("matching source rows=%d", got)
	}
	sqliteExec(t, db, "INSERT INTO entries VALUES (2, 'new')")
	for _, suffix := range []string{"-wal", "-shm"} {
		if got := sqlitefile.State(path + suffix); got != sqlitefile.StateOn {
			t.Fatalf("%s state=%v", suffix, got)
		}
	}

	rollbackPath := filepath.Join(dir, "rollback.sqlite")
	rollback := sqliteNew(t, rollbackPath)
	sqliteExec(t, rollback, "CREATE TABLE entries(value)", "PRAGMA journal_mode=PERSIST", "INSERT INTO entries VALUES (1)")
	if got := sqlitefile.State(rollbackPath + "-journal"); got != sqlitefile.StateOn {
		t.Fatalf("rollback journal state=%v", got)
	}
	t.Log("confirmed NoCOW on the directory, main database, WAL, shared memory and rollback journal")
}

func btrfsTestDir(t *testing.T) string {
	t.Helper()
	root := os.Getenv("SPL_SQLITEFILE_BTRFS_DIR")
	if root == "" {
		t.Skip("set SPL_SQLITEFILE_BTRFS_DIR to a writable scratch directory on Btrfs")
	}
	dir, err := os.MkdirTemp(root, "sqlitefile-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if got := sqlitefile.State(dir); got != sqlitefile.StateOff {
		t.Fatalf("expected CoW Btrfs for the fixture, got %v", got)
	}
	return dir
}
