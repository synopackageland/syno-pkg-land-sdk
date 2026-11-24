package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

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

// Priority orders waiting operations at each transaction boundary.
type Priority uint8

const (
	// Interactive is the default, for work a person or caller waits for.
	Interactive Priority = iota
	// Urgent runs before any other waiting operation, for small writes that
	// keep the App alive, such as a lease renewal.
	Urgent
	// Background is for bulk or periodic work. When Interactive and
	// Background operations both wait, the writer alternates between them.
	Background
	priorities
)

// WriteOptions schedules one write.
type WriteOptions struct {
	Priority Priority
	// Durability raises the commit's sync mode above the Store's default.
	// The commit uses the stronger of the two. A transaction never mixes
	// durabilities.
	Durability Durability
	// Bytes is the estimated cost for batching. An item larger than the
	// byte budget runs alone. Zero uses only the item limit.
	Bytes int
	// Interruptible runs the write in its own transaction and passes the
	// caller's context to its SQL, so cancellation interrupts a running
	// statement and rolls back only this write.
	Interruptible bool
	// Observe, when set, receives this write's WriteStats after
	// Options.Observe, on the caller's goroutine before WriteWith returns.
	Observe func(WriteStats)
}

// WriteStats describes how one write ran. Durations and times come from the
// writer; the transaction phases are shared by the callbacks of one group.
type WriteStats struct {
	Priority   Priority
	Durability Durability
	GroupSize  int
	// QueueWait runs from submission to the start of the group's work.
	QueueWait, Begin, Work, Commit time.Duration
	// CommitStarted and CommitFinished bound the group's COMMIT statement.
	CommitStarted, CommitFinished time.Time
	Failed                        bool
}

type operationKey struct{}
type itemKey struct{}

// Write queues a callback for an immediate transaction, possibly shared with
// other callbacks. A callback error rolls back its own work. The return value
// reports the callback error or the result of committing the transaction.
// Once started, a callback finishes before Write returns. Observe its context
// for cancellation in Go; SQL statements run without cancellation so SQLite
// cannot interrupt the transaction shared with other callers. Return on SQL
// errors. Callbacks must not recursively write, maintain or close the Store.
func (s *Store) Write(ctx context.Context, fn func(context.Context, Tx) error) error {
	return s.WriteWith(ctx, WriteOptions{}, fn)
}

// WriteWeighted is Write with an estimated byte cost for batching. An item
// larger than the byte budget runs alone. A cost of zero uses the item limit.
func (s *Store) WriteWeighted(ctx context.Context, bytes int, fn func(context.Context, Tx) error) error {
	return s.WriteWith(ctx, WriteOptions{Bytes: bytes}, fn)
}

// WriteWith is Write with a priority, durability and byte cost.
func (s *Store) WriteWith(ctx context.Context, opts WriteOptions, fn func(context.Context, Tx) error) error {
	if opts.Bytes < 0 || fn == nil || opts.Priority >= priorities || opts.Durability > Full {
		return errors.New("SQLite write callback, priority, durability and nonnegative byte cost required")
	}
	return s.submit(s.newJob(ctx, opts, func(j *job) { j.write = fn }))
}

// WriteTx runs fn in a database/sql transaction of its own on the writer,
// for code written against *sql.Tx. The transaction begins IMMEDIATE (see
// Options.DSNParams for drivers other than "sqlite" and "sqlite3"), follows
// the caller's context like an Interruptible write, and commits when fn
// returns nil. Bytes and Interruptible in opts are ignored. fn must not
// commit or roll back tx; AfterCommit works with the context fn receives.
func (s *Store) WriteTx(ctx context.Context, opts WriteOptions, fn func(context.Context, *sql.Tx) error) error {
	if fn == nil || opts.Priority >= priorities || opts.Durability > Full {
		return errors.New("SQLite transaction callback, priority and durability required")
	}
	return s.submit(s.newJob(ctx, opts, func(j *job) { j.tx = fn }))
}

func (s *Store) newJob(ctx context.Context, opts WriteOptions, set func(*job)) *job {
	j := &job{ctx: ctx, priority: opts.Priority, durability: max(opts.Durability, s.opts.Durability),
		bytes: opts.Bytes, interruptible: opts.Interruptible, observe: opts.Observe}
	set(j)
	return j
}

// AfterCommit registers fn from inside a write callback, using the context
// the callback received. The writer runs fn after the callback's transaction
// commits, in registration order, before any caller in that transaction
// returns and before the next transaction begins. fn is dropped when the
// callback's work rolls back or the commit fails. Keep fn short; it must not
// use the Store. Call AfterCommit before the callback returns.
func AfterCommit(ctx context.Context, fn func()) error {
	j, _ := ctx.Value(itemKey{}).(*job)
	if j == nil || fn == nil {
		return errors.New("AfterCommit requires a function and a write callback context")
	}
	j.hooks = append(j.hooks, fn)
	return nil
}

// Exclusive queues fn to run on the writer connection outside any
// transaction, between groups, with Interactive priority. Use it for work
// that needs the connection itself, such as migrations that control their own
// transaction, VACUUM or a checkpoint. fn must leave no transaction open; the
// Store rolls back a leftover transaction and restores its connection
// settings afterwards. fn receives the caller's context and must not use the
// Store.
func (s *Store) Exclusive(ctx context.Context, fn func(context.Context, *sql.Conn) error) error {
	if fn == nil {
		return errors.New("SQLite exclusive callback required")
	}
	return s.submit(&job{ctx: ctx, exclusive: fn})
}

// Maintain queues sqlitefile upkeep on the writer outside a transaction.
// Schedule it from the App when the database is quiet.
func (s *Store) Maintain(ctx context.Context) error {
	return s.Exclusive(ctx, sqlitefile.MaintainConn)
}

// MaintainWith is Maintain with explicit upkeep options.
func (s *Store) MaintainWith(ctx context.Context, opts sqlitefile.MaintainOptions) error {
	return s.Exclusive(ctx, func(ctx context.Context, conn *sql.Conn) error {
		return sqlitefile.MaintainConnWith(ctx, conn, opts)
	})
}

type job struct {
	ctx       context.Context
	write     func(context.Context, Tx) error // set for grouped and interruptible writes
	tx        func(context.Context, *sql.Tx) error
	exclusive func(context.Context, *sql.Conn) error
	// interruptible runs alone and passes the caller's context to its SQL.
	interruptible bool
	priority      Priority
	durability    Durability
	bytes         int
	observe       func(WriteStats)
	queuedAt      time.Time
	hooks         []func() // Owned by the writer goroutine.
	started       bool     // Protected by writeQueue.mu.
	done          chan result
}

// grouped reports whether the job may share a transaction.
func (j *job) grouped() bool { return j.write != nil && !j.interruptible }

type result struct {
	err        error
	panicValue any
	stats      *WriteStats
}

type writeQueue struct {
	mu      sync.Mutex
	items   [priorities][]*job
	closing bool
	// interactiveLast records which of Interactive and Background ran last.
	interactiveLast bool
	changed         chan struct{} // Closed and replaced whenever the queue changes.
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

func (q *writeQueue) empty() bool {
	for _, items := range q.items {
		if len(items) != 0 {
			return false
		}
	}
	return true
}

// Pending reports how many operations wait for the writer, not counting
// the transaction or exclusive work it is running.
func (s *Store) Pending() int {
	q := &s.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, items := range q.items {
		n += len(items)
	}
	return n
}

func (s *Store) submit(j *job) error {
	if j.ctx.Value(operationKey{}) == s {
		return errors.New("cannot queue an operation from a SQLite write callback")
	}
	j.done = make(chan result, 1)
	if s.observed(j) {
		j.queuedAt = time.Now()
	}
	if err := s.enqueue(j); err != nil {
		return err
	}
	select {
	case r := <-j.done:
		return s.finish(j, r)
	case <-j.ctx.Done():
		q := &s.queue
		q.mu.Lock()
		if !j.started {
			items := q.items[j.priority]
			i := slices.Index(items, j)
			q.items[j.priority] = slices.Delete(items, i, i+1)
			q.signal()
			q.mu.Unlock()
			return j.ctx.Err()
		}
		q.mu.Unlock()
		// The callback may still be running, or the group may be committing.
		return s.finish(j, <-j.done)
	}
}

func (s *Store) observed(j *job) bool { return s.opts.Observe != nil || j.observe != nil }

func (s *Store) finish(j *job, r result) error {
	if r.stats != nil {
		for _, fn := range []func(WriteStats){s.opts.Observe, j.observe} {
			if fn != nil {
				observe(fn, *r.stats)
			}
		}
	}
	if r.panicValue != nil {
		panic(r.panicValue)
	}
	return r.err
}

// An observer panic cannot change an outcome the writer already decided.
func observe(fn func(WriteStats), stats WriteStats) {
	defer func() { _ = recover() }()
	fn(stats)
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
		if len(q.items[j.priority]) < s.opts.QueueSize {
			q.items[j.priority] = append(q.items[j.priority], j)
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

// next chooses the queue for the next transaction: Urgent first, then
// Interactive and Background in turn while both wait.
func (q *writeQueue) next() Priority {
	if len(q.items[Urgent]) != 0 {
		return Urgent
	}
	interactive, background := len(q.items[Interactive]) != 0, len(q.items[Background]) != 0
	if interactive && (!background || !q.interactiveLast) {
		q.interactiveLast = true
		return Interactive
	}
	q.interactiveLast = false
	return Background
}

func (s *Store) nextBatch() []*job {
	q := &s.queue
	for {
		q.mu.Lock()
		if !q.empty() {
			p := q.next()
			items := q.items[p]
			first := items[0]
			batch, rest := []*job{first}, items[1:]
			if first.grouped() {
				rest = []*job{}
				bytes := first.bytes
				for i, j := range items[1:] {
					if !j.grouped() || len(batch) == s.opts.MaxBatchItems ||
						(j.durability == first.durability && j.bytes > s.opts.MaxBatchBytes-bytes) {
						// Keep FIFO order behind work that runs alone and an item
						// that does not fit.
						rest = append(rest, items[1+i:]...)
						break
					}
					if j.durability != first.durability {
						rest = append(rest, j)
						continue
					}
					batch = append(batch, j)
					bytes += j.bytes
				}
			}
			q.items[p] = rest
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
		if batch[0].exclusive != nil {
			batch[0].done <- result{err: s.withTurn(batch[0].ctx, func() error { return s.runExclusive(batch[0]) })}
			continue
		}
		var timing *groupTiming
		if slices.ContainsFunc(batch, s.observed) {
			timing = &groupTiming{started: time.Now()}
		}
		results := make([]result, len(batch))
		var err error
		if turnErr := s.withTurn(context.Background(), func() error {
			if batch[0].tx != nil {
				err = s.runTx(batch[0], &results[0], timing)
			} else {
				err = s.commit(batch, results, timing)
			}
			return nil
		}); turnErr != nil {
			err = turnErr
		}
		if err == nil {
			s.afterCommit(batch, results)
		}
		for i, j := range batch {
			if results[i].err == nil && results[i].panicValue == nil {
				results[i].err = err
			}
			if timing != nil && s.observed(j) {
				results[i].stats = timing.stats(j, len(batch), results[i])
			}
			j.done <- results[i]
		}
	}
}

func (s *Store) runExclusive(j *job) (err error) {
	if err := j.ctx.Err(); err != nil {
		return err
	}
	if err := s.ensureWriter(j.ctx); err != nil {
		return err
	}
	defer func() {
		// Leave the connection as the next group expects it: outside a
		// transaction (an error here means none was open), with the Store's
		// own settings.
		ctx := context.WithoutCancel(j.ctx)
		_, _ = s.writer.ExecContext(ctx, "ROLLBACK")
		if restoreErr := s.applyWriterPragmas(ctx, s.writer); restoreErr != nil {
			s.resetWriter()
			err = errors.Join(err, restoreErr)
		}
	}()
	err = j.exclusive(context.WithValue(j.ctx, operationKey{}, s), s.writer)
	if errors.Is(err, sql.ErrConnDone) {
		s.resetWriter()
		return err
	}
	return err
}

type groupTiming struct {
	started                       time.Time
	begin, work, commit           time.Duration
	commitStarted, commitFinished time.Time
}

func (t *groupTiming) stats(j *job, size int, r result) *WriteStats {
	return &WriteStats{Priority: j.priority, Durability: j.durability, GroupSize: size,
		QueueWait: t.started.Sub(j.queuedAt), Begin: t.begin, Work: t.work, Commit: t.commit,
		CommitStarted: t.commitStarted, CommitFinished: t.commitFinished,
		Failed: r.err != nil || r.panicValue != nil}
}

func (s *Store) resetWriter() {
	s.statements.reset()
	discard(s.writer)
	s.writer = nil
}

func (s *Store) commit(batch []*job, results []result, timing *groupTiming) (err error) {
	ctx := context.Background()
	if err := s.ensureWriter(ctx); err != nil {
		return err
	}
	if err := s.setSync(ctx, batch[0].durability); err != nil {
		return err
	}
	start := time.Now()
	if err := s.begin(ctx, batch); err != nil {
		if errors.Is(err, sql.ErrConnDone) {
			s.resetWriter()
		}
		if errors.Is(err, errAllCanceled) {
			for i, j := range batch {
				results[i].err = j.ctx.Err()
			}
			return nil
		}
		return fmt.Errorf("begin SQLite write: %w", err)
	}
	if timing != nil {
		timing.begin = time.Since(start)
		start = time.Now()
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.rollback(ctx))
		}
	}()
	if j := batch[0]; j.interruptible {
		// Alone in its transaction: an interrupt can roll back only this
		// write, so its SQL follows the caller's context.
		results[0] = call(s.callbackContext(j), itemTx{s, s.callbackContext(j)}, j.write)
		if results[0].err == nil {
			results[0].err = j.ctx.Err()
		}
		if results[0].err != nil || results[0].panicValue != nil {
			j.hooks = nil
			if err := s.rollback(ctx); err != nil {
				return err
			}
			return nil
		}
	} else {
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
	}
	if timing != nil {
		timing.work = time.Since(start)
		timing.commitStarted = time.Now()
	}
	err = s.retryBusy("COMMIT", func() error {
		_, err := s.writer.ExecContext(ctx, "COMMIT")
		return err
	}, nil)
	if timing != nil {
		timing.commitFinished = time.Now()
		timing.commit = timing.commitFinished.Sub(timing.commitStarted)
	}
	if err != nil {
		return fmt.Errorf("commit SQLite group: %w", err)
	}
	return nil
}

var errAllCanceled = errors.New("every write in the group was canceled")

// rollback ends the transaction on the writer. When SQLite already rolled
// it back (after an interrupt), there is nothing left to do.
func (s *Store) rollback(ctx context.Context) error {
	if s.writer == nil {
		return nil
	}
	if _, err := s.writer.ExecContext(ctx, "ROLLBACK"); err != nil && !strings.Contains(err.Error(), "no transaction is active") {
		s.resetWriter()
		return fmt.Errorf("roll back SQLite group: %w", err)
	}
	return nil
}

func (s *Store) callbackContext(j *job) context.Context {
	return context.WithValue(context.WithValue(j.ctx, operationKey{}, s), itemKey{}, j)
}

// begin waits for other processes' write locks without starting any
// callback, until every caller in the group gives up.
func (s *Store) begin(ctx context.Context, batch []*job) error {
	return s.retryBusy("BEGIN IMMEDIATE", func() error {
		_, err := s.writer.ExecContext(ctx, "BEGIN IMMEDIATE")
		return err
	}, func() bool {
		for _, j := range batch {
			if j.ctx.Err() == nil {
				return false
			}
		}
		return true
	})
}

// busyRetryDelay spaces attempts while another process holds SQLite's write
// lock after the busy handler gave up. It never fails or replays a callback.
const busyRetryDelay = 25 * time.Millisecond

// BusyAttempt describes a BEGIN IMMEDIATE or COMMIT that found the database
// locked by another connection after the busy timeout.
type BusyAttempt struct {
	Statement string // "BEGIN IMMEDIATE" or "COMMIT"
	Attempt   int    // 1 for the first busy result
	Err       error
}

// retryBusy repeats a statement that failed with SQLITE_BUSY. BEGIN
// IMMEDIATE has done nothing yet; a busy COMMIT keeps its transaction open.
func (s *Store) retryBusy(name string, statement func() error, abandon func() bool) error {
	for attempt := 1; ; attempt++ {
		err := statement()
		if !isBusy(err) {
			return err
		}
		if abandon != nil && abandon() {
			return errAllCanceled
		}
		delay := busyRetryDelay
		if s.opts.BusyRetry != nil {
			var retry bool
			if delay, retry = s.opts.BusyRetry(BusyAttempt{Statement: name, Attempt: attempt, Err: err}); !retry {
				return err
			}
		}
		time.Sleep(max(delay, 0))
	}
}

// Turn lets Stores of several database files take turns: Stores that share
// one Turn never run a transaction or exclusive work at the same time, and
// their writers wait in arrival order. Create it with NewTurn.
type Turn struct{ token chan struct{} }

// NewTurn returns a Turn to set in the Options of each participating Store.
func NewTurn() *Turn { return &Turn{token: make(chan struct{}, 1)} }

func (s *Store) withTurn(ctx context.Context, fn func() error) error {
	if t := s.opts.Turn; t != nil {
		select {
		case t.token <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-t.token }()
	}
	return fn()
}

// runTx runs a WriteTx job in a database/sql transaction of its own.
func (s *Store) runTx(j *job, r *result, timing *groupTiming) (err error) {
	ctx := s.callbackContext(j)
	if err := j.ctx.Err(); err != nil {
		r.err = err
		return nil
	}
	if err := s.ensureWriter(context.Background()); err != nil {
		return err
	}
	if err := s.setSync(context.Background(), j.durability); err != nil {
		return err
	}
	start := time.Now()
	var tx *sql.Tx
	if err := s.retryBusy("BEGIN IMMEDIATE", func() error {
		var err error
		tx, err = s.writer.BeginTx(ctx, nil)
		return err
	}, func() bool { return j.ctx.Err() != nil }); err != nil {
		if errors.Is(err, errAllCanceled) {
			r.err = j.ctx.Err()
			return nil
		}
		if errors.Is(err, sql.ErrConnDone) {
			s.resetWriter()
		}
		return fmt.Errorf("begin SQLite transaction: %w", err)
	}
	if timing != nil {
		timing.begin = time.Since(start)
		start = time.Now()
	}
	*r = callTx(ctx, tx, j.tx)
	if r.err == nil {
		r.err = j.ctx.Err()
	}
	if timing != nil {
		timing.work = time.Since(start)
	}
	if r.err != nil || r.panicValue != nil {
		j.hooks = nil
		_ = tx.Rollback()
		// database/sql may have ended the transaction on cancellation while
		// SQLite still holds it; leave the connection outside a transaction.
		return s.rollback(context.Background())
	}
	if timing != nil {
		timing.commitStarted = time.Now()
	}
	err = tx.Commit()
	if timing != nil {
		timing.commitFinished = time.Now()
		timing.commit = timing.commitFinished.Sub(timing.commitStarted)
	}
	if err != nil {
		return errors.Join(fmt.Errorf("commit SQLite transaction: %w", err), s.rollback(context.Background()))
	}
	return nil
}

func callTx(ctx context.Context, tx *sql.Tx, fn func(context.Context, *sql.Tx) error) (r result) {
	defer func() { r.panicValue = recover() }()
	r.err = fn(ctx, tx)
	return r
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		// SQLITE_BUSY and its extended codes, except BUSY_SNAPSHOT, which
		// needs a new transaction.
		return coded.Code()&0xff == 5 && coded.Code() != 517
	}
	return strings.Contains(err.Error(), "database is locked")
}

func (s *Store) setSync(ctx context.Context, d Durability) error {
	if s.sync == d {
		return nil
	}
	if _, err := s.writer.ExecContext(ctx, syncPragma(d)); err != nil {
		if errors.Is(err, sql.ErrConnDone) {
			s.resetWriter()
		}
		return fmt.Errorf("set SQLite write durability: %w", err)
	}
	s.sync = d
	return nil
}

func (s *Store) runItem(ctx context.Context, j *job) (result, error) {
	if _, err := s.writer.ExecContext(ctx, "SAVEPOINT sdk_item"); err != nil {
		return result{}, fmt.Errorf("start SQLite write savepoint: %w", err)
	}
	callbackCtx := s.callbackContext(j)
	r := call(callbackCtx, itemTx{s, context.WithoutCancel(callbackCtx)}, j.write)
	if r.err == nil {
		r.err = j.ctx.Err()
	}
	if r.err != nil || r.panicValue != nil {
		j.hooks = nil
		if _, err := s.writer.ExecContext(ctx, "ROLLBACK TO sdk_item"); err != nil {
			return r, fmt.Errorf("roll back SQLite write savepoint: %w", err)
		}
	}
	if _, err := s.writer.ExecContext(ctx, "RELEASE sdk_item"); err != nil {
		return r, fmt.Errorf("release SQLite write savepoint: %w", err)
	}
	return r, nil
}

// afterCommit runs the hooks of a committed group in order. A hook panic is
// re-raised in its caller; the other hooks still run.
func (s *Store) afterCommit(batch []*job, results []result) {
	for i, j := range batch {
		for _, fn := range j.hooks {
			func() {
				defer func() {
					if p := recover(); p != nil && results[i].panicValue == nil {
						results[i].panicValue = p
					}
				}()
				fn()
			}()
		}
		j.hooks = nil
	}
}

func call(ctx context.Context, tx Tx, fn func(context.Context, Tx) error) (r result) {
	defer func() { r.panicValue = recover() }()
	r.err = fn(ctx, tx)
	return r
}

// Use the caller's context values while keeping SQLite interrupt local to the
// writer's lifecycle. Cancellation is checked before and after each callback.
// Single statements are prepared once per writer connection.
type itemTx struct {
	s   *Store
	ctx context.Context
}

func (tx itemTx) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if stmt := tx.s.statements.get(tx.ctx, tx.s.writer, query); stmt != nil {
		return stmt.ExecContext(tx.ctx, args...)
	}
	return tx.s.writer.ExecContext(tx.ctx, query, args...)
}

func (tx itemTx) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	if stmt := tx.s.statements.get(tx.ctx, tx.s.writer, query); stmt != nil {
		return stmt.QueryContext(tx.ctx, args...)
	}
	return tx.s.writer.QueryContext(tx.ctx, query, args...)
}

func (tx itemTx) QueryRowContext(_ context.Context, query string, args ...any) *sql.Row {
	if stmt := tx.s.statements.get(tx.ctx, tx.s.writer, query); stmt != nil {
		return stmt.QueryRowContext(tx.ctx, args...)
	}
	return tx.s.writer.QueryRowContext(tx.ctx, query, args...)
}

// statementLimit bounds the prepared statements kept for the writer.
const statementLimit = 256

// statementCache keeps compiled single statements of the current writer
// connection. Only the writer goroutine uses it.
type statementCache struct {
	stmts map[string]*sql.Stmt
}

// get returns a prepared statement, or nil to run the query directly: a
// script, a full cache or a statement that fails to prepare (running it
// directly reports the same error).
func (c *statementCache) get(ctx context.Context, conn *sql.Conn, query string) *sql.Stmt {
	if stmt := c.stmts[query]; stmt != nil {
		return stmt
	}
	if len(c.stmts) >= statementLimit || strings.Contains(query, ";") {
		return nil
	}
	stmt, err := conn.PrepareContext(ctx, query)
	if err != nil {
		return nil
	}
	if c.stmts == nil {
		c.stmts = make(map[string]*sql.Stmt)
	}
	c.stmts[query] = stmt
	return stmt
}

func (c *statementCache) reset() {
	for _, stmt := range c.stmts {
		_ = stmt.Close()
	}
	c.stmts = nil
}
