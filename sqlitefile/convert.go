package sqlitefile

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const stagingPrefix = ".sqlitefile-"
const spaceHeadroom = 1 << 20

// ErrInsufficientSpace indicates that the preflight free-space check failed.
var ErrInsufficientSpace = errors.New("insufficient free space for SQLite NoCOW conversion")

// OpenDB returns a new pool for the exact given existing database path. The
// converter owns the returned pool and closes it. The opener must allow writes
// and load any schema extensions needed by the database. It must not run App
// migrations, attach other databases, or change journal or locking modes.
type OpenDB func(path string) (*sql.DB, error)

// ConvertToNoCOW rebuilds a CoW database in its NoCOW directory and atomically
// replaces it. The caller must hold an App-wide exclusive lock and close all
// database handles, including readers, until this call returns. The directory
// must be App-controlled and names starting with .sqlitefile- are reserved.
//
// changed reports whether the rename took place, including when only the
// subsequent directory fsync fails. A false, nil result means no work was
// needed or the filesystem or App policy does not permit NoCOW.
func ConvertToNoCOW(ctx context.Context, path string, open OpenDB) (changed bool, err error) {
	if optedOut() {
		return false, nil
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return false, err
	}
	dir := filepath.Dir(path)
	eligible, err := eligibleDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if !eligible {
		return false, err
	}

	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if mode := info.Mode(); !mode.IsRegular() || mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !hasSingleLink(info) {
		return false, errors.New("SQLite conversion requires a regular database file with one hard link and ordinary permissions")
	}
	state, err := probeState(path)
	if err != nil || state == StateOn {
		return false, err
	}
	if state != StateOff {
		return false, errors.New("cannot determine NoCOW state of SQLite database")
	}
	if open == nil {
		return false, errors.New("SQLite conversion requires a database opener")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	base := filepath.Base(path)
	if strings.HasPrefix(base, stagingPrefix) {
		return false, errors.New("SQLite database uses a name reserved for conversion")
	}
	if err := checkSidecars(path, false); err != nil {
		return false, err
	}

	staging := filepath.Join(dir, fmt.Sprintf("%s%x", stagingPrefix, sha256.Sum256([]byte(base))))
	if err := removeStaging(staging); err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, removeStaging(staging)) }()

	err = withConn(ctx, open, path, func(conn *sql.Conn) error {
		if err := checkpoint(ctx, conn); err != nil {
			return err
		}
		checkpointed, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := checkSpace(dir, uint64(checkpointed.Size())); err != nil {
			return err
		}

		// SQLite allows an existing empty output. Create it privately and
		// verify that the directory's NoCOW flag was inherited before writing.
		f, err := os.OpenFile(staging, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if State(staging) != StateOn {
			return errors.New("SQLite conversion output did not inherit NoCOW")
		}
		if _, err := conn.ExecContext(ctx, "VACUUM main INTO ?", staging); err != nil {
			return fmt.Errorf("vacuum SQLite database into NoCOW file: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if err := checkSidecars(path, true); err != nil {
		return false, err
	}
	if err := withConn(ctx, open, staging, func(conn *sql.Conn) error {
		var result string
		if err := conn.QueryRowContext(ctx, "PRAGMA main.quick_check(1)").Scan(&result); err != nil {
			return fmt.Errorf("quick check of SQLite replacement: %w", err)
		}
		if result != "ok" {
			return fmt.Errorf("quick check of SQLite replacement: %s", result)
		}
		return nil
	}); err != nil {
		return false, err
	}
	if err := checkSidecars(staging, true); err != nil {
		return false, err
	}
	if err := syncReplacement(path, staging, info); err != nil {
		return false, err
	}

	parent, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	// Make the checkpointed source's sidecar removals durable before the
	// database name can refer to a different inode.
	if err := parent.Sync(); err != nil {
		return false, fmt.Errorf("sync SQLite directory before replacement: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := os.Rename(staging, path); err != nil {
		return false, err
	}
	if err := parent.Sync(); err != nil {
		return true, fmt.Errorf("sync SQLite directory after replacement: %w", err)
	}
	return true, nil
}

func withConn(ctx context.Context, open OpenDB, path string, run func(*sql.Conn) error) (err error) {
	db, err := open(path)
	if err != nil {
		return err
	}
	if db == nil {
		return errors.New("SQLite opener returned a nil database pool")
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	return run(conn)
}

func checkpoint(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "PRAGMA main.synchronous=FULL"); err != nil {
		return fmt.Errorf("enable full sync for SQLite conversion: %w", err)
	}
	var busy, walPages, checkpointed int
	if err := conn.QueryRowContext(ctx, "PRAGMA main.wal_checkpoint(TRUNCATE)").Scan(&busy, &walPages, &checkpointed); err != nil {
		return fmt.Errorf("checkpoint SQLite WAL: %w", err)
	}
	// SQLite reports (0, -1, -1) when the DB is not in WAL mode.
	if busy != 0 || walPages != checkpointed || walPages > 0 {
		return fmt.Errorf("SQLite WAL checkpoint did not truncate (busy=%d, log=%d, checkpointed=%d)", busy, walPages, checkpointed)
	}
	return nil
}

func checkSpace(dir string, dbBytes uint64) error {
	free, err := availableBytes(dir)
	if err != nil {
		return fmt.Errorf("check space for SQLite conversion: %w", err)
	}
	if free < dbBytes || free-dbBytes < spaceHeadroom {
		return fmt.Errorf("%w: %d bytes available for a %d-byte database plus %d bytes of headroom",
			ErrInsufficientSpace, free, dbBytes, spaceHeadroom)
	}
	return nil
}

var sidecarSuffixes = [...]string{"-wal", "-shm", "-journal"}

func checkSidecars(path string, mustBeAbsent bool) error {
	for _, suffix := range sidecarSuffixes {
		sidecar := path + suffix
		info, err := os.Lstat(sidecar)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if mustBeAbsent {
			return fmt.Errorf("SQLite sidecar %s remains after closing the database", sidecar)
		}
		if !info.Mode().IsRegular() || !hasSingleLink(info) {
			return fmt.Errorf("unexpected SQLite sidecar %s", sidecar)
		}
	}
	return nil
}

func removeStaging(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		name := path + suffix
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || !hasSingleLink(info) {
			return fmt.Errorf("unexpected file at SQLite conversion path %s", name)
		}
		if err := os.Remove(name); err != nil {
			return err
		}
	}
	return nil
}

func syncReplacement(path, staging string, original os.FileInfo) error {
	old, err := os.Open(path)
	if err != nil {
		return err
	}
	defer old.Close()
	current, err := old.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(current, original) {
		return errors.New("SQLite database was replaced during conversion")
	}
	result, err := os.Open(staging)
	if err != nil {
		return err
	}
	defer result.Close()
	if err := result.Chmod(original.Mode().Perm()); err != nil {
		return err
	}
	info, err := result.Stat()
	if err != nil {
		return err
	}
	if err := checkReplacementMetadata(old, result, current, info); err != nil {
		return err
	}
	if err := result.Sync(); err != nil {
		return fmt.Errorf("sync SQLite replacement: %w", err)
	}
	return nil
}
