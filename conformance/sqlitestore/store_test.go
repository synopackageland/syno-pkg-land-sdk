package sqlitestore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitestore"
	_ "modernc.org/sqlite"
)

func openStore(t testing.TB, opts sqlitestore.Options) *sqlitestore.Store {
	t.Helper()
	opts.DriverName = "sqlite"
	path := filepath.Join(t.TempDir(), "private", "state #?%.db")
	s, err := sqlitestore.Open(t.Context(), path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := exec(t.Context(), s, "CREATE TABLE records (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	return s
}

func exec(ctx context.Context, s *sqlitestore.Store, query string, args ...any) error {
	return s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	})
}

func count(t testing.TB, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM records").Scan(&got); err != nil || got != want {
		t.Fatalf("row count = %d, err = %v, want %d", got, err, want)
	}
}

func TestPoolsAndPragmas(t *testing.T) {
	s := openStore(t, sqlitestore.Options{
		Durability: sqlitestore.Full, BusyTimeoutMS: 1234, Readers: 2, BackgroundReaders: 1,
		WriterCacheKiB: 512, ReaderCacheKiB: 256, BackgroundCacheKiB: 64,
	})
	ctx := t.Context()
	check := func(q sqlitestore.Tx, settings map[string]int) {
		t.Helper()
		for key, want := range settings {
			var got int
			if err := q.QueryRowContext(ctx, "PRAGMA "+key).Scan(&got); err != nil || got != want {
				t.Errorf("%s = %d, err = %v, want %d", key, got, err, want)
			}
		}
	}
	if err := s.Write(ctx, func(_ context.Context, tx sqlitestore.Tx) error {
		check(tx, map[string]int{"synchronous": 2, "busy_timeout": 1234, "foreign_keys": 1,
			"trusted_schema": 0, "temp_store": 1, "cache_size": -512,
			"journal_size_limit": 64 << 20, "wal_autocheckpoint": 1000, "query_only": 0})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, pool := range []struct {
		db       *sql.DB
		n, cache int
	}{{s.Reader, 2, 256}, {s.BackgroundReader, 1, 64}} {
		if got := pool.db.Stats().MaxOpenConnections; got != pool.n {
			t.Fatalf("pool limit = %d, want %d", got, pool.n)
		}
		for range pool.n {
			c, err := pool.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			check(c, map[string]int{"busy_timeout": 1234, "foreign_keys": 1,
				"trusted_schema": 0, "temp_store": 1, "cache_size": -pool.cache, "query_only": 1})
			if _, err := c.ExecContext(ctx, "PRAGMA query_only=OFF"); err != nil {
				t.Fatal(err)
			}
			if _, err := c.ExecContext(ctx, "INSERT INTO records VALUES (1)"); err == nil {
				t.Fatal("SQLite URI allowed a write after query_only was turned off")
			}
		}
	}
	// The background connection stays checked out while the writer works.
	if err := exec(ctx, s, "INSERT INTO records VALUES (2)"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	s := openStore(t, sqlitestore.Options{BackgroundReaders: 1})
	ctx := t.Context()
	snapshot, err := s.BackgroundReader.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	var before int
	if err := snapshot.QueryRowContext(ctx, "SELECT count(*) FROM records").Scan(&before); err != nil || before != 0 {
		t.Fatalf("initial snapshot = %d, %v", before, err)
	}
	var group sync.WaitGroup
	for i := 1; i <= 100; i++ {
		group.Go(func() {
			if err := exec(ctx, s, "INSERT INTO records VALUES (?)", i); err != nil {
				t.Error(err)
			}
		})
		group.Go(func() {
			var n int
			if err := s.Reader.QueryRowContext(ctx, "SELECT count(*) FROM records").Scan(&n); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	var after int
	if err := snapshot.QueryRowContext(ctx, "SELECT count(*) FROM records").Scan(&after); err != nil || after != before {
		t.Fatalf("held snapshot = %d, %v", after, err)
	}
	count(t, s.Reader, 100)
	if err := s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestGroupIsolationAndWorkBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := openStore(t, sqlitestore.Options{MaxBatchItems: 3, MaxBatchBytes: 10})
		ctx := t.Context()
		hold := make(chan struct{})
		started := make(chan struct{})
		first := make(chan error, 1)
		go func() {
			first <- s.Write(ctx, func(context.Context, sqlitestore.Tx) error {
				close(started)
				<-hold
				return nil
			})
		}()
		<-started

		refused := errors.New("refused")
		aborted := errors.New("aborted callback")
		results := make(chan error, 5)
		for _, item := range []struct{ id, bytes, prior int }{
			{1, 5, 0}, {2, 5, 0}, {3, 6, 1}, {4, 4, 1}, {5, 11, 3},
		} {
			go func() {
				results <- s.WriteWeighted(ctx, item.bytes, func(ctx context.Context, tx sqlitestore.Tx) error {
					var visible int
					if err := s.Reader.QueryRowContext(ctx, "SELECT count(*) FROM records").Scan(&visible); err != nil {
						return err
					}
					if visible != item.prior {
						return fmt.Errorf("item %d sees %d committed rows, want %d", item.id, visible, item.prior)
					}
					if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (?)", item.id); err != nil {
						return err
					}
					if item.id == 2 {
						return refused
					}
					return nil
				})
			}()
			synctest.Wait()
		}
		close(hold)
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		refusals := 0
		for range 5 {
			if err := <-results; errors.Is(err, refused) {
				refusals++
			} else if err != nil {
				t.Fatal(err)
			}
		}
		if refusals != 1 {
			t.Fatalf("refusals = %d, want 1", refusals)
		}
		count(t, s.Reader, 4)
		err := s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
			_, err := tx.ExecContext(ctx, "INSERT OR ROLLBACK INTO records VALUES (1)")
			if err == nil {
				t.Error("duplicate insert succeeded")
			}
			return aborted
		})
		if !errors.Is(err, aborted) {
			t.Fatalf("aborted callback got %v", err)
		}
		if err := exec(ctx, s, "INSERT INTO records VALUES (6)"); err != nil {
			t.Fatalf("writer did not recover after transaction rollback: %v", err)
		}
	})
}

func TestBackpressureCancelAndCloseDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := openStore(t, sqlitestore.Options{QueueSize: 1})
		ctx := t.Context()
		release := make(chan struct{})
		started := make(chan struct{})
		active, queued, blocked := make(chan error, 1), make(chan error, 1), make(chan error, 1)
		go func() {
			active <- s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
				close(started)
				<-release
				_, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (1)")
				return err
			})
		}()
		<-started
		cancelCtx, cancel := context.WithCancel(ctx)
		go func() { queued <- exec(cancelCtx, s, "INSERT INTO records VALUES (2)") }()
		synctest.Wait()
		go func() { blocked <- exec(ctx, s, "INSERT INTO records VALUES (3)") }()
		synctest.Wait()
		cancel()
		if err := <-queued; !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancel = %v", err)
		}
		synctest.Wait() // The waiter takes the slot vacated by cancellation.
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		synctest.Wait()
		if err := exec(ctx, s, "INSERT INTO records VALUES (4)"); !errors.Is(err, sqlitestore.ErrClosed) {
			t.Fatalf("late write = %v", err)
		}
		close(release)
		for _, done := range []chan error{active, blocked, closed} {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
	})
}

func BenchmarkConcurrentWrites(b *testing.B) {
	for _, limit := range []int{1, 32} {
		b.Run(fmt.Sprintf("batch-%d", limit), func(b *testing.B) {
			s := openStore(b, sqlitestore.Options{MaxBatchItems: limit})
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if err := exec(b.Context(), s, "INSERT INTO records DEFAULT VALUES"); err != nil {
						b.Error(err)
					}
				}
			})
		})
	}
}

func TestConcurrentOpenAndImmediateWriter(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "fresh.db")
	const stores = 8
	start := make(chan struct{})
	errs := make(chan error, stores)
	var group sync.WaitGroup
	for range stores {
		group.Go(func() {
			<-start
			s, err := sqlitestore.Open(ctx, path, sqlitestore.Options{DriverName: "sqlite"})
			if err == nil {
				err = exec(ctx, s, "CREATE TABLE IF NOT EXISTS records (id INTEGER PRIMARY KEY)")
				err = errors.Join(err, s.Close())
			}
			errs <- err
		})
	}
	close(start)
	group.Wait()
	for range stores {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	s, err := sqlitestore.Open(ctx, path, sqlitestore.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	external, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	if err := s.Write(ctx, func(ctx context.Context, _ sqlitestore.Tx) error {
		// The callback has executed no SQL. The other connection must still
		// be unable to get a write transaction.
		if _, err := external.ExecContext(ctx, "BEGIN IMMEDIATE"); err == nil {
			_, _ = external.ExecContext(ctx, "ROLLBACK")
			return errors.New("second connection got a write lock during callback")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledAndPanickingCallbacksKeepNeighbors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := openStore(t, sqlitestore.Options{})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		holding := make(chan struct{})
		started := make(chan struct{})
		first := make(chan error, 1)
		go func() {
			first <- s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
				close(started)
				<-holding
				return nil
			})
		}()
		<-started
		canceled, success := make(chan error, 1), make(chan error, 1)
		panicked := make(chan any, 1)
		go func() {
			canceled <- s.Write(t.Context(), func(ctx context.Context, tx sqlitestore.Tx) error {
				writeCtx, cancelWrite := context.WithCancel(ctx)
				cancelWrite()
				_, err := tx.ExecContext(writeCtx, "INSERT INTO records VALUES (1)")
				if err != nil {
					t.Errorf("item SQL was interrupted: %v", err)
				}
				return writeCtx.Err()
			})
		}()
		synctest.Wait()
		go func() {
			defer func() { panicked <- recover() }()
			_ = s.Write(t.Context(), func(ctx context.Context, tx sqlitestore.Tx) error {
				if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (2)"); err != nil {
					return err
				}
				panic("deliberate")
			})
		}()
		synctest.Wait()
		go func() { success <- exec(t.Context(), s, "INSERT INTO records VALUES (3)") }()
		synctest.Wait()
		close(holding)
		if err := <-first; err != nil {
			t.Error(err)
		}
		if err := <-canceled; !errors.Is(err, context.Canceled) {
			t.Errorf("canceled callback = %v", err)
		}
		if got := <-panicked; got != "deliberate" {
			t.Errorf("panic = %v", got)
		}
		if err := <-success; err != nil {
			t.Error(err)
		}
		count(t, s.Reader, 1)
	})
}
