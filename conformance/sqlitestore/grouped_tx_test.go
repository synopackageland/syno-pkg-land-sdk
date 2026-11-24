package sqlitestore_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitestore"
)

func TestGroupedNativeTransactionsIsolateNestedSavepoints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := openStore(t, sqlitestore.Options{})
		release, held := hold(t, s)
		ctx := t.Context()
		refused := errors.New("refused")
		errs := make(chan error, 3)
		var shared *sql.Tx
		var hooks []int
		for _, id := range []int{1, 2, 3} {
			go func() {
				errs <- s.WriteGroupedTx(ctx, sqlitestore.WriteOptions{}, func(ctx context.Context, tx *sql.Tx) error {
					if shared == nil {
						shared = tx
					}
					if shared != tx || ctx.Done() != nil {
						return errors.New("expected one shared native transaction and noninterruptible SQL context")
					}
					if _, err := tx.ExecContext(ctx, "SAVEPOINT nested"); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (?)", id); err != nil {
						return err
					}
					if err := sqlitestore.AfterCommit(ctx, func() { hooks = append(hooks, id) }); err != nil {
						return err
					}
					if id == 2 {
						return refused // The SDK must unwind the nested savepoint, too.
					}
					_, err := tx.ExecContext(ctx, "RELEASE nested")
					return err
				})
			}()
			synctest.Wait()
		}
		release()
		if err := <-held; err != nil {
			t.Fatal(err)
		}
		var refusals int
		for range 3 {
			if err := <-errs; errors.Is(err, refused) {
				refusals++
			} else if err != nil {
				t.Fatal(err)
			}
		}
		if refusals != 1 || !slices.Equal(hooks, []int{1, 3}) {
			t.Fatalf("refusals = %d, hooks = %v", refusals, hooks)
		}
		var ids string
		if err := s.Reader.QueryRowContext(ctx, "SELECT group_concat(id ORDER BY id) FROM records").Scan(&ids); err != nil || ids != "1,3" {
			t.Fatalf("committed IDs = %q, err = %v", ids, err)
		}
	})
}

func TestWaitingPriorityAgesByWriterDispatchAndPendingCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := openStore(t, sqlitestore.Options{MaxBatchItems: 1})
		release, held := hold(t, s)
		ctx := t.Context()
		results := make(chan error, 13)
		firstStarted, firstDone := make(chan struct{}), make(chan struct{})
		var order []int
		queue := func(priority sqlitestore.Priority, id int) {
			go func() {
				results <- s.WriteWith(ctx, sqlitestore.WriteOptions{Priority: priority}, func(context.Context, sqlitestore.Tx) error {
					if id == 1 {
						close(firstStarted)
						<-firstDone
					}
					order = append(order, id)
					return nil
				})
			}()
			synctest.Wait()
		}
		queue(sqlitestore.Background, 0)
		for id := 1; id <= 8; id++ {
			queue(sqlitestore.Urgent, id)
		}
		counts := s.PendingByPriority()
		release()
		<-firstStarted
		for id := 9; id <= 12; id++ {
			queue(sqlitestore.Urgent, id)
		}
		close(firstDone)
		if err := <-held; err != nil {
			t.Fatal(err)
		}
		for range 13 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		if counts[sqlitestore.Background] != 1 || counts[sqlitestore.Urgent] != 8 || counts[sqlitestore.Interactive] != 0 {
			t.Fatalf("waiting counts = %v", counts)
		}
		if want := []int{1, 2, 3, 4, 5, 6, 7, 8, 0, 9, 10, 11, 12}; !slices.Equal(order, want) {
			t.Fatalf("dispatch order = %v, want %v", order, want)
		}
	})
}

func TestBusyCommitRetriesWithoutReplayingCallbacks(t *testing.T) {
	type key struct{}
	for _, mode := range []string{"native-retry", "grouped-give-up", "interface-cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, mode))
			defer cancel()
			uri := url.URL{Scheme: "file", Path: filepath.Join(t.TempDir(), "attached.db")}
			attached, err := sql.Open("sqlite", uri.String())
			if err != nil {
				t.Fatal(err)
			}
			defer attached.Close()
			if _, err := attached.ExecContext(ctx, "PRAGMA journal_mode=DELETE; CREATE TABLE items (id INTEGER)"); err != nil {
				t.Fatal(err)
			}
			proceed := make(chan struct{})
			attempts := make(chan sqlitestore.BusyAttempt, 2)
			s := openStore(t, sqlitestore.Options{
				BusyTimeoutMS:  -1,
				ConnectionInit: []string{"ATTACH DATABASE '" + uri.String() + "' AS g"},
				BusyRetry: func(a sqlitestore.BusyAttempt) (time.Duration, bool) {
					attempts <- a
					if a.Statement != "COMMIT" {
						return 0, false
					}
					switch mode {
					case "native-retry":
						if a.Attempt == 1 {
							<-proceed
							return 0, true
						}
					case "grouped-give-up":
						return 0, a.Attempt < 2
					case "interface-cancel":
						return time.Hour, true
					}
					return 0, false
				},
			})
			// A rollback-journal attachment allows the write to start and
			// blocks COMMIT until this separate connection releases its read lock.
			reader, err := attached.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Rollback()
			var initial int
			if err := reader.QueryRowContext(ctx, "SELECT count(*) FROM items").Scan(&initial); err != nil {
				t.Fatal(err)
			}
			calls, committed := 0, false
			write := func(ctx context.Context, tx sqlitestore.Tx) error {
				calls++
				if _, err := tx.ExecContext(ctx, "INSERT INTO g.items VALUES (7)"); err != nil {
					return err
				}
				return sqlitestore.AfterCommit(ctx, func() { committed = true })
			}
			native := func(ctx context.Context, tx *sql.Tx) error { return write(ctx, tx) }
			done := make(chan error, 1)
			go func() {
				switch mode {
				case "native-retry":
					done <- s.WriteTx(ctx, sqlitestore.WriteOptions{}, native)
				case "grouped-give-up":
					done <- s.WriteGroupedTx(ctx, sqlitestore.WriteOptions{}, native)
				case "interface-cancel":
					done <- s.Write(ctx, write)
				}
			}()
			check := func(a sqlitestore.BusyAttempt, want int) {
				t.Helper()
				if a.Statement != "COMMIT" || a.Attempt != want || a.Context.Value(key{}) != mode {
					t.Errorf("busy attempt = %+v, want COMMIT attempt %d with caller context", a, want)
				}
			}
			if mode == "grouped-give-up" {
				err = <-done
				for i := 1; i <= 2; i++ {
					select {
					case a := <-attempts:
						check(a, i)
					default:
						t.Errorf("missing COMMIT attempt %d", i)
					}
				}
			} else {
				select {
				case a := <-attempts:
					check(a, 1)
				case err := <-done:
					t.Fatalf("write returned before busy attempt: %v", err)
				}
				if mode == "native-retry" {
					if err := reader.Rollback(); err != nil {
						t.Error(err)
					}
					close(proceed)
				} else {
					cancel()
				}
				err = <-done
			}
			wantCommit := mode == "native-retry"
			if mode == "interface-cancel" && !errors.Is(err, context.Canceled) {
				t.Errorf("canceled COMMIT = %v", err)
			}
			if mode == "grouped-give-up" && (err == nil || !strings.Contains(err.Error(), "database is locked")) {
				t.Errorf("COMMIT refusal = %v", err)
			}
			if wantCommit && err != nil {
				t.Error(err)
			}
			if calls != 1 || committed != wantCommit {
				t.Errorf("callback calls = %d, commit hook = %v", calls, committed)
			}
			_ = reader.Rollback()
			var saved int
			if err := attached.QueryRowContext(t.Context(), "SELECT count(*) FROM items").Scan(&saved); err != nil || (saved == 1) != wantCommit {
				t.Errorf("saved rows = %d, err = %v", saved, err)
			}
			if err := exec(t.Context(), s, "INSERT INTO records VALUES (1)"); err != nil {
				t.Fatalf("next transaction: %v", err)
			}
		})
	}
}
