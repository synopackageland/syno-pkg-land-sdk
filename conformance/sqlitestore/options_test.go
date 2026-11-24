package sqlitestore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitefile"
	"github.com/synopackageland/syno-pkg-land-sdk/sqlitestore"
)

func TestConnectionInitAttachesOnEveryConnection(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.db")
	catalog, err := sqlitestore.Open(ctx, catalogPath, sqlitestore.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec(ctx, catalog, "CREATE TABLE items (name TEXT); INSERT INTO items VALUES ('a')"); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	attach := url.URL{Scheme: "file", Path: filepath.ToSlash(catalogPath), RawQuery: "mode=ro"}
	s := openStore(t, sqlitestore.Options{BackgroundReaders: 1, JournalSizeLimit: 1 << 20,
		ConnectionInit: []string{"ATTACH DATABASE '" + attach.String() + "' AS g", "PRAGMA g.cache_size=-64"}})
	if err := s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
		if got := pragma(t, ctx, tx, "journal_size_limit"); got != 1<<20 {
			return fmt.Errorf("journal_size_limit = %d", got)
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO records SELECT count(*) FROM g.items")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO g.items VALUES ('b')"); err == nil {
			return errors.New("read-only attachment accepted a write")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, db := range []*sql.DB{s.Reader, s.BackgroundReader} {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM g.items").Scan(&n); err != nil || n != 1 {
			t.Fatalf("reader sees %d attached rows, err %v", n, err)
		}
	}
	if _, err := sqlitestore.Open(ctx, filepath.Join(dir, "x.db"), sqlitestore.Options{DriverName: "sqlite",
		DSNParams: url.Values{"mode": {"rwc"}}}); err == nil {
		t.Fatal("DSNParams overrode mode")
	}
}

func TestOverflowReadersCloseWhenIdle(t *testing.T) {
	s := openStore(t, sqlitestore.Options{Readers: 1, OverflowReaders: 2})
	ctx := t.Context()
	var conns []*sql.Conn
	for range 3 {
		c, err := s.Reader.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	if got := s.Reader.Stats().OpenConnections; got != 3 {
		t.Fatalf("open = %d, want 3", got)
	}
	for _, c := range conns {
		_ = c.Close()
	}
	if got := s.Reader.Stats().OpenConnections; got != 1 {
		t.Fatalf("open after release = %d, want 1", got)
	}
}

func TestInterruptibleWriteRunsAloneAndStopsOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var sizes []int
		s := openStore(t, sqlitestore.Options{Observe: func(v sqlitestore.WriteStats) {
			mu.Lock()
			sizes = append(sizes, v.GroupSize)
			mu.Unlock()
		}})
		ctx := t.Context()
		release, first := hold(t, s)
		results := make(chan error, 3)
		for i := range 3 {
			go func() {
				results <- s.WriteWith(ctx, sqlitestore.WriteOptions{Interruptible: i == 1},
					func(ctx context.Context, tx sqlitestore.Tx) error {
						_, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (?)", i)
						return err
					})
			}()
			synctest.Wait()
		}
		release()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		// Created table, held write, then 1 + 1 + 1: the interruptible write
		// splits the queue and runs alone, keeping FIFO order.
		if fmt.Sprint(sizes[2:]) != "[1 1 1]" {
			t.Fatalf("group sizes = %v", sizes)
		}
	})
	s := openStore(t, sqlitestore.Options{})
	ctx, cancel := context.WithCancel(t.Context())
	err := s.WriteWith(ctx, sqlitestore.WriteOptions{Interruptible: true}, func(ctx context.Context, tx sqlitestore.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (1)"); err != nil {
			return err
		}
		cancel()
		_, err := tx.ExecContext(ctx, "WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n) INSERT INTO records SELECT x+10 FROM n")
		return err
	})
	if err == nil {
		t.Fatal("canceled interruptible write committed")
	}
	count(t, s.Reader, 0)
	if err := exec(t.Context(), s, "INSERT INTO records VALUES (2)"); err != nil {
		t.Fatalf("writer after interrupt: %v", err)
	}
}

func TestWriteTxCommitsRollsBackAndBeginsImmediate(t *testing.T) {
	s := openStore(t, sqlitestore.Options{})
	ctx := t.Context()
	hooked := false
	if err := s.WriteTx(ctx, sqlitestore.WriteOptions{Durability: sqlitestore.Full}, func(ctx context.Context, tx *sql.Tx) error {
		other, err := otherDB(t, s)
		if err != nil {
			return err
		}
		defer other.Close()
		if _, err := other.ExecContext(ctx, "BEGIN IMMEDIATE"); err == nil {
			return errors.New("WriteTx did not take the write lock at BEGIN")
		}
		var sync int
		if err := tx.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil || sync != 2 {
			return fmt.Errorf("synchronous = %d, %v", sync, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (1)"); err != nil {
			return err
		}
		return sqlitestore.AfterCommit(ctx, func() { hooked = true })
	}); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("refused")
	if err := s.WriteTx(ctx, sqlitestore.WriteOptions{}, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (2)"); err != nil {
			return err
		}
		return refused
	}); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	if err := s.WriteTx(cctx, sqlitestore.WriteOptions{}, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (3)"); err != nil {
			return err
		}
		cancel()
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled WriteTx = %v", err)
	}
	count(t, s.Reader, 1)
	if !hooked {
		t.Fatal("AfterCommit did not run for WriteTx")
	}
	if err := exec(ctx, s, "INSERT INTO records VALUES (4)"); err != nil {
		t.Fatal(err)
	}
}

func TestBusyRetryHookCanGiveUp(t *testing.T) {
	ctx := t.Context()
	var attempts []sqlitestore.BusyAttempt
	s := openStore(t, sqlitestore.Options{BusyTimeoutMS: -1, BusyRetry: func(a sqlitestore.BusyAttempt) (time.Duration, bool) {
		attempts = append(attempts, a)
		return time.Millisecond, a.Attempt < 3
	}})
	other, err := otherDB(t, s)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := s.Write(ctx, func(context.Context, sqlitestore.Tx) error { ran = true; return nil }); err == nil || ran {
		t.Fatalf("busy write = %v, ran %v", err, ran)
	}
	if len(attempts) != 3 || attempts[0].Statement != "BEGIN IMMEDIATE" || attempts[2].Attempt != 3 {
		t.Fatalf("attempts = %+v", attempts)
	}
	_, _ = conn.ExecContext(ctx, "ROLLBACK")
	if err := exec(ctx, s, "INSERT INTO records VALUES (1)"); err != nil {
		t.Fatal(err)
	}
}

func TestStoresSharingATurnNeverWriteAtOnce(t *testing.T) {
	turn := sqlitestore.NewTurn()
	a := openStore(t, sqlitestore.Options{Turn: turn})
	b := openStore(t, sqlitestore.Options{Turn: turn})
	ctx := t.Context()
	var mu sync.Mutex
	active, overlap := 0, false
	work := func(s *sqlitestore.Store) error {
		return s.Write(ctx, func(context.Context, sqlitestore.Tx) error {
			mu.Lock()
			active++
			overlap = overlap || active > 1
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			return nil
		})
	}
	var group sync.WaitGroup
	for range 20 {
		for _, s := range []*sqlitestore.Store{a, b} {
			group.Go(func() {
				if err := work(s); err != nil {
					t.Error(err)
				}
			})
		}
	}
	group.Wait()
	if overlap {
		t.Fatal("stores sharing a Turn wrote at the same time")
	}
}

func TestMaintainWithBoundsWork(t *testing.T) {
	s := openStore(t, sqlitestore.Options{})
	ctx := t.Context()
	if err := s.Exclusive(ctx, func(ctx context.Context, conn *sql.Conn) error {
		for _, q := range []string{"PRAGMA auto_vacuum=INCREMENTAL", "VACUUM", "CREATE TABLE pad (v BLOB)",
			"INSERT INTO pad SELECT randomblob(4000) FROM (WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n LIMIT 200) SELECT x FROM n)",
			"DELETE FROM pad"} {
			if _, err := conn.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	free := func() (n int) {
		if err := s.Reader.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := free()
	if err := s.MaintainWith(ctx, sqlitefile.MaintainOptions{OptimizeMask: 0x10002, AnalysisLimit: 400, VacuumPages: 10}); err != nil {
		t.Fatal(err)
	}
	// Ten pages plus the freelist trunk pages that held them; never all.
	if after := free(); before-after < 10 || before-after > 20 {
		t.Fatalf("freelist %d -> %d, want about 10 pages reclaimed", before, after)
	}
	if err := s.Exclusive(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var limit int
		if err := conn.QueryRowContext(ctx, "PRAGMA analysis_limit").Scan(&limit); err != nil || limit != 0 {
			return fmt.Errorf("analysis_limit left at %d, %v", limit, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
