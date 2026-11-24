package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitefile"
)

// ErrClosed is returned when the Store no longer accepts operations.
var ErrClosed = errors.New("SQLite store is closed")

// Tx is the SQL interface available to a write callback. Finish using it and
// close all rows before returning. Transaction and savepoint control, changes
// to connection settings, and statements that roll back the whole transaction
// (such as INSERT OR ROLLBACK) are reserved to the Store.
type Tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type operationKey struct{}

// Write queues a callback for an immediate transaction, possibly shared with
// other callbacks. A callback error rolls back its own work. The return value
// reports the callback error or the result of committing the transaction.
// Once started, a callback finishes before Write returns. Observe its context
// for cancellation in Go; SQL statements run without cancellation so SQLite
// cannot interrupt the transaction shared with other callers. Return on SQL
// errors. Callbacks must not recursively write, maintain or close the Store.
func (s *Store) Write(ctx context.Context, fn func(context.Context, Tx) error) error {
	return s.WriteWeighted(ctx, 0, fn)
}

// WriteWeighted is Write with an estimated byte cost for batching. An item
// larger than the byte budget runs alone. A cost of zero uses the item limit.
func (s *Store) WriteWeighted(ctx context.Context, bytes int, fn func(context.Context, Tx) error) error {
	if bytes < 0 || fn == nil {
		return errors.New("SQLite write callback and nonnegative byte cost required")
	}
	return s.submit(&job{ctx: ctx, bytes: bytes, write: fn})
}

// Maintain queues sqlitefile upkeep on the writer outside a transaction.
// Schedule it from the App when the database is quiet.
func (s *Store) Maintain(ctx context.Context) error {
	return s.submit(&job{ctx: ctx})
}

type job struct {
	ctx     context.Context
	write   func(context.Context, Tx) error // nil for maintenance
	bytes   int
	started bool // Protected by writeQueue.mu.
	done    chan result
}

type result struct {
	err        error
	panicValue any
}

type writeQueue struct {
	mu      sync.Mutex
	items   []*job
	closing bool
	changed chan struct{} // Closed and replaced whenever the queue changes.
}

func (q *writeQueue) signal() {
	close(q.changed)
	q.changed = make(chan struct{})
}

func (q *writeQueue) stop() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closing = true
	q.signal()
}

func (s *Store) submit(j *job) error {
	if j.ctx.Value(operationKey{}) == s {
		return errors.New("cannot queue an operation from a SQLite write callback")
	}
	j.done = make(chan result, 1)
	if err := s.enqueue(j); err != nil {
		return err
	}
	select {
	case r := <-j.done:
		return r.unwrap()
	case <-j.ctx.Done():
		q := &s.queue
		q.mu.Lock()
		if !j.started {
			i := slices.Index(q.items, j)
			q.items = slices.Delete(q.items, i, i+1)
			q.signal()
			q.mu.Unlock()
			return j.ctx.Err()
		}
		q.mu.Unlock()
		// The callback may still be running, or the group may be committing.
		return (<-j.done).unwrap()
	}
}

func (r result) unwrap() error {
	if r.panicValue != nil {
		panic(r.panicValue)
	}
	return r.err
}

func (s *Store) enqueue(j *job) error {
	q := &s.queue
	for {
		if err := j.ctx.Err(); err != nil {
			return err
		}
		q.mu.Lock()
		if q.closing {
			q.mu.Unlock()
			return ErrClosed
		}
		if len(q.items) < s.opts.QueueSize {
			q.items = append(q.items, j)
			q.signal()
			q.mu.Unlock()
			return nil
		}
		changed := q.changed
		q.mu.Unlock()
		select {
		case <-changed:
		case <-j.ctx.Done():
			return j.ctx.Err()
		}
	}
}

func (s *Store) nextBatch() []*job {
	q := &s.queue
	for {
		q.mu.Lock()
		if len(q.items) != 0 {
			first := q.items[0]
			n, bytes := 1, first.bytes
			if first.write != nil {
				for _, j := range q.items[1:min(s.opts.MaxBatchItems, len(q.items))] {
					if j.write == nil || j.bytes > s.opts.MaxBatchBytes-bytes {
						break
					}
					n++
					bytes += j.bytes
				}
			}
			batch := slices.Clone(q.items[:n])
			q.items = slices.Delete(q.items, 0, n)
			for _, j := range batch {
				j.started = true
			}
			q.signal()
			q.mu.Unlock()
			return batch
		}
		if q.closing {
			q.mu.Unlock()
			return nil
		}
		changed := q.changed
		q.mu.Unlock()
		<-changed
	}
}

func (s *Store) run() {
	defer close(s.done)
	for batch := s.nextBatch(); len(batch) > 0; batch = s.nextBatch() {
		if batch[0].write == nil {
			batch[0].done <- result{err: s.maintain(batch[0].ctx)}
			continue
		}
		results := make([]result, len(batch))
		err := s.commit(batch, results)
		for i, j := range batch {
			if results[i].err == nil && results[i].panicValue == nil {
				results[i].err = err
			}
			j.done <- results[i]
		}
	}
}

func (s *Store) maintain(ctx context.Context) error {
	if err := s.ensureWriter(ctx); err != nil {
		return err
	}
	err := sqlitefile.MaintainConn(ctx, s.writer)
	if errors.Is(err, sql.ErrConnDone) {
		s.resetWriter()
	}
	return err
}

func (s *Store) resetWriter() {
	discard(s.writer)
	s.writer = nil
}

func (s *Store) commit(batch []*job, results []result) (err error) {
	ctx := context.Background()
	if err := s.ensureWriter(ctx); err != nil {
		return err
	}
	if _, err := s.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if errors.Is(err, sql.ErrConnDone) {
			s.resetWriter()
		}
		return fmt.Errorf("begin SQLite write: %w", err)
	}
	defer func() {
		if err != nil {
			if _, rollbackErr := s.writer.ExecContext(ctx, "ROLLBACK"); rollbackErr != nil {
				s.resetWriter()
				err = errors.Join(err, fmt.Errorf("roll back SQLite group: %w", rollbackErr))
			}
		}
	}()
	for i, j := range batch {
		if err := j.ctx.Err(); err != nil {
			results[i].err = err
			continue
		}
		r, err := s.runItem(ctx, j)
		results[i] = r
		if err != nil {
			return err
		}
	}
	if _, err := s.writer.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit SQLite group: %w", err)
	}
	return nil
}

func (s *Store) runItem(ctx context.Context, j *job) (result, error) {
	if _, err := s.writer.ExecContext(ctx, "SAVEPOINT sdk_item"); err != nil {
		return result{}, fmt.Errorf("start SQLite write savepoint: %w", err)
	}
	callbackCtx := context.WithValue(j.ctx, operationKey{}, s)
	r := call(callbackCtx, itemTx{s.writer, context.WithoutCancel(callbackCtx)}, j.write)
	if r.err == nil {
		r.err = j.ctx.Err()
	}
	if r.err != nil || r.panicValue != nil {
		if _, err := s.writer.ExecContext(ctx, "ROLLBACK TO sdk_item"); err != nil {
			return r, fmt.Errorf("roll back SQLite write savepoint: %w", err)
		}
	}
	if _, err := s.writer.ExecContext(ctx, "RELEASE sdk_item"); err != nil {
		return r, fmt.Errorf("release SQLite write savepoint: %w", err)
	}
	return r, nil
}

func call(ctx context.Context, tx Tx, fn func(context.Context, Tx) error) (r result) {
	defer func() { r.panicValue = recover() }()
	r.err = fn(ctx, tx)
	return r
}

// Use the caller's context values while keeping SQLite interrupt local to the
// writer's lifecycle. Cancellation is checked before and after each callback.
type itemTx struct {
	conn *sql.Conn
	ctx  context.Context
}

func (tx itemTx) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	return tx.conn.ExecContext(tx.ctx, query, args...)
}

func (tx itemTx) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.conn.QueryContext(tx.ctx, query, args...)
}

func (tx itemTx) QueryRowContext(_ context.Context, query string, args ...any) *sql.Row {
	return tx.conn.QueryRowContext(tx.ctx, query, args...)
}
