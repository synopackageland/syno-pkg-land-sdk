package sqlitestore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitestore"
)

// hold occupies the writer until the returned release is called.
func hold(t *testing.T, s *sqlitestore.Store) (release func(), done chan error) {
	t.Helper()
	started, gate, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Write(t.Context(), func(context.Context, sqlitestore.Tx) error {
			close(started)
			<-gate
			return nil
		})
	}()
	<-started
	return func() { close(gate) }, done
}

func pragma(t *testing.T, ctx context.Context, q sqlitestore.Tx, name string) int {
	t.Helper()
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPerWriteDurabilityNeverSharesATransaction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var stats []sqlitestore.WriteStats
		s := openStore(t, sqlitestore.Options{Observe: func(v sqlitestore.WriteStats) {
			mu.Lock()
			stats = append(stats, v)
			mu.Unlock()
		}})
		ctx := t.Context()
		release, first := hold(t, s)
		results := make(chan error, 6)
		for i := range 6 {
			durability := sqlitestore.Durability(i % 2)
			go func() {
				results <- s.WriteWith(ctx, sqlitestore.WriteOptions{Durability: durability},
					func(ctx context.Context, tx sqlitestore.Tx) error {
						if got := pragma(t, ctx, tx, "synchronous"); got != int(durability)+1 {
							return fmt.Errorf("durability %d commits with synchronous=%d", durability, got)
						}
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
		for range 6 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		count(t, s.Reader, 6)
		mu.Lock()
		groups := map[sqlitestore.Durability]int{}
		for _, v := range stats[1:] {
			groups[v.Durability] = max(groups[v.Durability], v.GroupSize)
		}
		mu.Unlock()
		if groups[sqlitestore.Normal] != 3 || groups[sqlitestore.Full] != 3 {
			t.Fatalf("groups by durability = %v, want three of each", groups)
		}
		// The Store default applies again after a stronger write.
		if err := s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
			if got := pragma(t, ctx, tx, "synchronous"); got != 1 {
				return fmt.Errorf("default write runs with synchronous=%d", got)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestStoreDurabilityIsAFloor(t *testing.T) {
	s := openStore(t, sqlitestore.Options{Durability: sqlitestore.Full})
	if err := s.WriteWith(t.Context(), sqlitestore.WriteOptions{Durability: sqlitestore.Normal},
		func(ctx context.Context, tx sqlitestore.Tx) error {
			if got := pragma(t, ctx, tx, "synchronous"); got != 2 {
				return fmt.Errorf("synchronous = %d, want FULL", got)
			}
			return nil
		}); err != nil {
		t.Fatal(err)
	}
}

func TestPriorityOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := openStore(t, sqlitestore.Options{MaxBatchItems: 1})
		ctx := t.Context()
		release, first := hold(t, s)
		var mu sync.Mutex
		var order []string
		results := make(chan error, 6)
		for _, item := range []struct {
			name     string
			priority sqlitestore.Priority
		}{
			{"b1", sqlitestore.Background}, {"b2", sqlitestore.Background}, {"b3", sqlitestore.Background},
			{"i1", sqlitestore.Interactive}, {"i2", sqlitestore.Interactive}, {"u", sqlitestore.Urgent},
		} {
			go func() {
				results <- s.WriteWith(ctx, sqlitestore.WriteOptions{Priority: item.priority},
					func(context.Context, sqlitestore.Tx) error {
						mu.Lock()
						order = append(order, item.name)
						mu.Unlock()
						return nil
					})
			}()
			synctest.Wait()
		}
		release()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		for range 6 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		// Urgent first; then Interactive and Background alternate, starting
		// with Background because the held write was Interactive.
		if want := []string{"u", "b1", "i1", "b2", "i2", "b3"}; !slices.Equal(order, want) {
			t.Fatalf("order = %v, want %v", order, want)
		}
	})
}

func TestQueueLimitIsPerPriority(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := openStore(t, sqlitestore.Options{QueueSize: 1})
		ctx := t.Context()
		release, first := hold(t, s)
		queued := make(chan error, 1)
		go func() { queued <- exec(ctx, s, "INSERT INTO records VALUES (1)") }()
		synctest.Wait()
		if got := s.Pending(); got != 1 {
			t.Fatalf("Pending = %d, want 1", got)
		}
		waiting := make(chan error, 1)
		go func() { waiting <- exec(ctx, s, "INSERT INTO records VALUES (2)") }()
		urgent := make(chan error, 1)
		go func() {
			urgent <- s.WriteWith(ctx, sqlitestore.WriteOptions{Priority: sqlitestore.Urgent},
				func(ctx context.Context, tx sqlitestore.Tx) error {
					_, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (3)")
					return err
				})
		}()
		synctest.Wait()
		release()
		for _, done := range []chan error{first, queued, waiting, urgent} {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		count(t, s.Reader, 3)
	})
}

func TestAfterCommitRunsOnlyForCommittedWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := openStore(t, sqlitestore.Options{})
		ctx := t.Context()
		if err := sqlitestore.AfterCommit(ctx, func() {}); err == nil {
			t.Fatal("AfterCommit outside a write callback succeeded")
		}
		release, first := hold(t, s)
		var mu sync.Mutex
		var ran []string
		var visible []int
		record := func(name string) func() {
			return func() {
				mu.Lock()
				defer mu.Unlock()
				ran = append(ran, name)
				var n int
				if err := s.Reader.QueryRowContext(context.Background(), "SELECT count(*) FROM records").Scan(&n); err != nil {
					t.Error(err)
				}
				visible = append(visible, n)
			}
		}
		refused := errors.New("refused")
		results := make(chan error, 3)
		for _, item := range []struct {
			id  int
			err error
		}{{1, nil}, {2, refused}, {3, nil}} {
			go func() {
				results <- s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
					if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (?)", item.id); err != nil {
						return err
					}
					if err := sqlitestore.AfterCommit(ctx, record(fmt.Sprint(item.id, "a"))); err != nil {
						return err
					}
					if err := sqlitestore.AfterCommit(ctx, record(fmt.Sprint(item.id, "b"))); err != nil {
						return err
					}
					return item.err
				})
			}()
			synctest.Wait()
		}
		release()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if err := <-results; err != nil && !errors.Is(err, refused) {
				t.Fatal(err)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if want := []string{"1a", "1b", "3a", "3b"}; !slices.Equal(ran, want) {
			t.Fatalf("hooks = %v, want %v", ran, want)
		}
		for _, n := range visible {
			if n != 2 {
				t.Fatalf("hook saw %d committed rows, want the group's 2", n)
			}
		}
	})
}

func TestAfterCommitPanicReachesItsCaller(t *testing.T) {
	s := openStore(t, sqlitestore.Options{})
	defer func() {
		if p := recover(); p != "hook" {
			t.Fatalf("panic = %v", p)
		}
		count(t, s.Reader, 1)
	}()
	_ = s.Write(t.Context(), func(ctx context.Context, tx sqlitestore.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (1)"); err != nil {
			return err
		}
		return sqlitestore.AfterCommit(ctx, func() { panic("hook") })
	})
	t.Fatal("hook panic was not re-raised")
}

func TestExclusiveRunsOutsideTransactionsAndRestoresSettings(t *testing.T) {
	s := openStore(t, sqlitestore.Options{})
	ctx := t.Context()
	if err := exec(ctx, s, "INSERT INTO records VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Exclusive(ctx, func(ctx context.Context, conn *sql.Conn) error {
		for _, q := range []string{"BEGIN IMMEDIATE", "INSERT INTO records VALUES (2)", "COMMIT", "VACUUM",
			"PRAGMA synchronous=OFF", "PRAGMA foreign_keys=OFF", "BEGIN", "INSERT INTO records VALUES (3)"} {
			if _, err := conn.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}
		// The open transaction is left behind deliberately.
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	count(t, s.Reader, 2)
	if err := s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
		if pragma(t, ctx, tx, "synchronous") != 1 || pragma(t, ctx, tx, "foreign_keys") != 1 {
			return errors.New("exclusive work changed the writer's settings")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
		return s.Exclusive(ctx, func(context.Context, *sql.Conn) error { return nil })
	}); err == nil {
		t.Fatal("exclusive work queued from a write callback")
	}
	if err := s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureWriterPreparesEachConnection(t *testing.T) {
	calls := 0
	s := openStore(t, sqlitestore.Options{ConfigureWriter: func(ctx context.Context, conn *sql.Conn) error {
		calls++
		_, err := conn.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS seen (id INTEGER)")
		return err
	}})
	ctx := t.Context()
	if err := s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO temp.seen VALUES (1)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("ConfigureWriter ran %d times", calls)
	}
	_, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "x.db"), sqlitestore.Options{DriverName: "sqlite",
		ConfigureWriter: func(context.Context, *sql.Conn) error { return errors.New("refused") }})
	if err == nil {
		t.Fatal("Open ignored a ConfigureWriter error")
	}
}

func TestBusyBeginWaitsWithoutReplayAndHonorsCancellation(t *testing.T) {
	ctx := t.Context()
	s := openStore(t, sqlitestore.Options{BusyTimeoutMS: -1})
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
	canceled, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	ran := 0
	err = s.Write(canceled, func(context.Context, sqlitestore.Tx) error { ran++; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || ran != 0 {
		t.Fatalf("canceled busy write = %v after %d callbacks", err, ran)
	}
	done := make(chan error, 1)
	go func() {
		done <- s.Write(ctx, func(ctx context.Context, tx sqlitestore.Tx) error {
			ran++
			_, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (1)")
			return err
		})
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || ran != 1 {
		t.Fatalf("busy write = %v after %d callbacks", err, ran)
	}
}

// otherDB opens a second, non-waiting connection pool on the store's file.
func otherDB(t *testing.T, s *sqlitestore.Store) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(dbPath(t, s)), RawQuery: "_pragma=busy_timeout(0)"}
	return sql.Open("sqlite", uri.String())
}

func dbPath(t *testing.T, s *sqlitestore.Store) string {
	t.Helper()
	var path string
	if err := s.Reader.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestObserveReportsGroupPhases(t *testing.T) {
	var stats []sqlitestore.WriteStats
	s := openStore(t, sqlitestore.Options{Observe: func(v sqlitestore.WriteStats) {
		stats = append(stats, v)
		panic("ignored")
	}})
	refused := errors.New("refused")
	var own []sqlitestore.WriteStats
	err := s.WriteWith(t.Context(), sqlitestore.WriteOptions{Durability: sqlitestore.Full, Priority: sqlitestore.Urgent,
		Observe: func(v sqlitestore.WriteStats) { own = append(own, v) }},
		func(context.Context, sqlitestore.Tx) error { return refused })
	if !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if len(own) != 1 || !own[0].Failed {
		t.Fatalf("per-write stats = %+v", own)
	}
	if err := exec(t.Context(), s, "INSERT INTO records VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if len(stats) != 3 { // Includes the table creation.
		t.Fatalf("stats = %d", len(stats))
	}
	failed, ok := stats[1], stats[2]
	if !failed.Failed || failed.Durability != sqlitestore.Full || failed.Priority != sqlitestore.Urgent || failed.GroupSize != 1 {
		t.Fatalf("failed write stats = %+v", failed)
	}
	if ok.Failed || ok.CommitFinished.Before(ok.CommitStarted) || ok.CommitStarted.IsZero() || ok.QueueWait < 0 {
		t.Fatalf("write stats = %+v", ok)
	}
}

func TestOpenReaderAddsAnOwnedPool(t *testing.T) {
	s, err := sqlitestore.Open(t.Context(), filepath.Join(t.TempDir(), "x.db"), sqlitestore.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := exec(ctx, s, "CREATE TABLE records (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	db, err := s.OpenReader(ctx, sqlitestore.ReaderOptions{Conns: 3, CacheKiB: 128})
	if err != nil {
		t.Fatal(err)
	}
	if got := db.Stats().MaxOpenConnections; got != 3 {
		t.Fatalf("pool limit = %d", got)
	}
	var cache, queryOnly int
	if err := db.QueryRowContext(ctx, "SELECT (SELECT cache_size FROM pragma_cache_size),(SELECT query_only FROM pragma_query_only)").Scan(&cache, &queryOnly); err != nil {
		t.Fatal(err)
	}
	if cache != -128 || queryOnly != 1 {
		t.Fatalf("cache=%d query_only=%d", cache, queryOnly)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO records VALUES (1)"); err == nil {
		t.Fatal("extra reader wrote")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(ctx); err == nil {
		t.Fatal("extra reader survived Close")
	}
	if _, err := s.OpenReader(ctx, sqlitestore.ReaderOptions{}); !errors.Is(err, sqlitestore.ErrClosed) {
		t.Fatalf("OpenReader after Close = %v", err)
	}
}

func TestWriterReusesManyStatements(t *testing.T) {
	s := openStore(t, sqlitestore.Options{})
	ctx := t.Context()
	for i := range 300 {
		if err := exec(ctx, s, fmt.Sprintf("INSERT INTO records VALUES (%d)", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := exec(ctx, s, "INSERT INTO records VALUES (?)", 1000); err != nil {
		t.Fatal(err)
	}
	if err := exec(ctx, s, "ALTER TABLE records ADD COLUMN note TEXT"); err != nil {
		t.Fatal(err)
	}
	if err := exec(ctx, s, "INSERT INTO records VALUES (?)", 1001); err == nil {
		t.Fatal("cached statement ignored a schema change")
	}
	if err := exec(ctx, s, "INSERT INTO records(id) VALUES (?)", 1001); err != nil {
		t.Fatal(err)
	}
	count(t, s.Reader, 302)
}
