package sqlitestore_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitestore"
)

// Callers that arrive together at an idle writer share one transaction, so
// a burst of durable writes pays one commit instead of two.
func TestSimultaneousBurstAtIdleWriterCommitsOnce(t *testing.T) {
	const bursts, callers = 50, 5
	var mu sync.Mutex
	var sizes []int
	s := openStore(t, sqlitestore.Options{Observe: func(v sqlitestore.WriteStats) {
		mu.Lock()
		sizes = append(sizes, v.GroupSize)
		mu.Unlock()
	}})
	ctx := t.Context()
	split := 0
	for b := range bursts {
		mu.Lock()
		sizes = sizes[:0]
		mu.Unlock()
		start := make(chan struct{})
		var group sync.WaitGroup
		for i := range callers {
			group.Go(func() {
				<-start
				if err := s.WriteWith(ctx, sqlitestore.WriteOptions{Durability: sqlitestore.Full},
					func(ctx context.Context, tx sqlitestore.Tx) error {
						_, err := tx.ExecContext(ctx, "INSERT INTO records VALUES (?)", b*callers+i)
						return err
					}); err != nil {
					t.Error(err)
				}
			})
		}
		close(start)
		group.Wait()
		mu.Lock()
		for _, n := range sizes {
			if n != callers {
				split++
				break
			}
		}
		mu.Unlock()
	}
	count(t, s.Reader, bursts*callers)
	// Goroutine scheduling is up to the runtime: a caller it has not run yet
	// when the group forms joins the next one. Without gathering nearly every
	// burst splits; with it most share one commit.
	if split > bursts*3/5 {
		t.Fatalf("%d of %d simultaneous bursts split into more than one commit", split, bursts)
	}
}

// A lone caller at an idle writer starts at once: no timer, no waiting for
// callers that do not exist. The bubble's clock never advances.
func TestLoneCallerIsNotDelayed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var stats []sqlitestore.WriteStats
		s := openStore(t, sqlitestore.Options{Observe: func(v sqlitestore.WriteStats) { stats = append(stats, v) }})
		synctest.Wait()
		if err := exec(t.Context(), s, "INSERT INTO records VALUES (1)"); err != nil {
			t.Fatal(err)
		}
		last := stats[len(stats)-1]
		if last.GroupSize != 1 || last.QueueWait != 0 {
			t.Fatalf("lone write stats = %+v", last)
		}
	})
}
