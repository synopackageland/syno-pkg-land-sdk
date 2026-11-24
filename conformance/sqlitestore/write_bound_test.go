package sqlitestore_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitestore"
	"modernc.org/sqlite"
)

func TestBoundNativeCancellationDuringSQL(t *testing.T) {
	for _, stage := range []string{"BEGIN", "INSERT INTO records VALUES (2)", "COMMIT"} {
		t.Run(stage, func(t *testing.T) {
			type key struct{}
			started := make(chan struct{})
			driverName := fmt.Sprintf("sqlite-context-%d", boundDriverID.Add(1))
			sql.Register(driverName, &boundDriver{atSQL: func(ctx context.Context, query string) error {
				if query != stage || ctx.Value(key{}) == nil {
					return nil
				}
				close(started)
				if ctx.Done() == nil {
					return errors.New("SQL ran with a detached context")
				}
				<-ctx.Done()
				return ctx.Err()
			}})
			s := openStore(t, sqlitestore.Options{
				DriverName: driverName,
				DSNParams:  url.Values{"_txlock": {"immediate"}},
			})
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, true))
			defer cancel()
			var calls, hooks int
			done := make(chan error, 1)
			go func() {
				done <- s.WriteTx(ctx, sqlitestore.WriteOptions{BindCallerContext: true}, func(ctx context.Context, tx *sql.Tx) error {
					calls++
					if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (1)"); err != nil {
						return err
					}
					if err := sqlitestore.AfterCommit(ctx, func() { hooks++ }); err != nil {
						return err
					}
					_, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (2)")
					return err
				})
			}()
			select {
			case <-started:
				cancel()
			case err := <-done:
				t.Fatalf("write ended before %s: %v", stage, err)
			}
			wantCalls := 1
			if stage == "BEGIN" {
				wantCalls = 0
			}
			if err := <-done; !errors.Is(err, context.Canceled) || calls != wantCalls || hooks != 0 {
				t.Fatalf("err=%v, calls=%d (want %d), hooks=%d", err, calls, wantCalls, hooks)
			}
			count(t, s.Reader, 0)
			if err := exec(t.Context(), s, "INSERT INTO records VALUES (3)"); err != nil {
				t.Fatalf("writer after cancellation: %v", err)
			}
			if err := s.WriteTx(t.Context(), sqlitestore.WriteOptions{BindCallerContext: true},
				func(ctx context.Context, tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (4)")
					return err
				}); err != nil {
				t.Fatal(err)
			}
			count(t, s.Reader, 2)
		})
	}
}

var boundDriverID atomic.Uint64

type boundDriver struct {
	sqlite.Driver
	atSQL func(context.Context, string) error
}

func (d *boundDriver) Open(dsn string) (driver.Conn, error) {
	conn, err := d.Driver.Open(dsn)
	if err != nil {
		return nil, err
	}
	return &boundConn{Conn: conn, atSQL: d.atSQL}, nil
}

type boundConn struct {
	driver.Conn
	atSQL func(context.Context, string) error
}

func (c *boundConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if err := c.atSQL(ctx, "BEGIN"); err != nil {
		return nil, err
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *boundConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.atSQL(ctx, query); err != nil {
		return nil, err
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func TestBoundGroupedWriteRunsAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := openStore(t, sqlitestore.Options{})
		release, held := hold(t, s)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		results := make([]chan error, 4)
		sizes := make([]int, len(results))
		hooks := []int{}
		inserted, resume := make(chan struct{}), make(chan struct{})
		var canceledStatement error
		for i := range results {
			results[i] = make(chan error, 1)
			go func() {
				opts := sqlitestore.WriteOptions{
					BindCallerContext: i == 1,
					Observe:           func(stats sqlitestore.WriteStats) { sizes[i] = stats.GroupSize },
				}
				itemCtx := t.Context()
				if i == 1 {
					itemCtx = ctx
				}
				results[i] <- s.WriteGroupedTx(itemCtx, opts, func(ctx context.Context, tx *sql.Tx) error {
					if (ctx.Done() != nil) != opts.BindCallerContext {
						return errors.New("wrong callback context")
					}
					if _, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (?)", i); err != nil {
						return err
					}
					if err := sqlitestore.AfterCommit(ctx, func() { hooks = append(hooks, i) }); err != nil {
						return err
					}
					if opts.BindCallerContext {
						close(inserted)
						<-resume
						_, canceledStatement = tx.ExecContext(ctx, "INSERT INTO records VALUES (99)")
					}
					return nil
				})
			}()
			synctest.Wait()
		}
		release()
		if err := <-held; err != nil {
			t.Fatal(err)
		}
		<-inserted
		cancel()
		synctest.Wait()
		// database/sql ends the bound transaction even while the callback is
		// still running. Verify the SQLite write lock from another connection.
		other, err := otherDB(t, s)
		if err == nil {
			_, err = other.ExecContext(t.Context(), "BEGIN IMMEDIATE; ROLLBACK")
			_ = other.Close()
		}
		close(resume)
		if err != nil {
			t.Errorf("write lock still held after cancellation: %v", err)
		}
		for i, done := range results {
			if err := <-done; (i == 1 && !errors.Is(err, context.Canceled)) || (i != 1 && err != nil) {
				t.Errorf("write %d: %v", i, err)
			}
		}
		if !errors.Is(canceledStatement, context.Canceled) {
			t.Fatalf("write after cancellation: %v", canceledStatement)
		}
		if !slices.Equal(sizes, []int{1, 1, 2, 2}) || !slices.Equal(hooks, []int{0, 2, 3}) {
			t.Fatalf("group sizes = %v, hooks = %v", sizes, hooks)
		}
		var ids string
		if err := s.Reader.QueryRowContext(t.Context(), "SELECT group_concat(id ORDER BY id) FROM records").Scan(&ids); err != nil || ids != "0,2,3" {
			t.Fatalf("committed IDs = %q, err = %v", ids, err)
		}
	})
}

func TestMaxBatchWork(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprintf("native=%v", native), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := openStore(t, sqlitestore.Options{MaxBatchWork: 5, MaxBatchBytes: 5, MaxBatchItems: 2})
				release, held := hold(t, s)
				costs := [][2]int{{4, 1}, {2, 1}, {3, 1}, {0, 1}, {6, 1}, {1, 4}, {1, 2}, {1, 1}, {1, 1}}
				done := make(chan error, len(costs))
				sizes := make([]int, len(costs))
				var order []int
				for i, cost := range costs {
					go func() {
						opts := sqlitestore.WriteOptions{Work: cost[0], Bytes: cost[1],
							Observe: func(stats sqlitestore.WriteStats) { sizes[i] = stats.GroupSize }}
						if native {
							done <- s.WriteGroupedTx(t.Context(), opts, func(context.Context, *sql.Tx) error {
								order = append(order, i)
								return nil
							})
						} else {
							done <- s.WriteWith(t.Context(), opts, func(context.Context, sqlitestore.Tx) error {
								order = append(order, i)
								return nil
							})
						}
					}()
					synctest.Wait()
				}
				release()
				if err := <-held; err != nil {
					t.Fatal(err)
				}
				for range costs {
					if err := <-done; err != nil {
						t.Fatal(err)
					}
				}
				if !slices.Equal(order, []int{0, 1, 2, 3, 4, 5, 6, 7, 8}) ||
					!slices.Equal(sizes, []int{1, 2, 2, 1, 1, 1, 2, 2, 1}) {
					t.Fatalf("order = %v, group sizes = %v", order, sizes)
				}
			})
		})
	}
}
