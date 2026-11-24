package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	// Urgent normally runs first, for small writes that keep the App alive,
	// such as a lease renewal. Aged lower-priority work can run first.
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
	// byte budget runs alone. Zero declares no byte cost.
	Bytes int
	// Work is the callback's declared count in the Store's MaxBatchWork
	// unit. When MaxBatchWork is set, zero or oversized work runs alone.
	Work int
	// Interruptible runs the write in its own transaction and passes the
	// caller's context to its SQL, so cancellation interrupts a running
	// statement and rolls back only this write.
	Interruptible bool
	// BindCallerContext binds a native transaction's BEGIN, lifetime and
	// COMMIT to the caller's context. Pass the callback context to SQL
	// methods to interrupt running statements. WriteGroupedTx runs a
	// bound callback alone so a SQLite interrupt cannot roll back peers.
	BindCallerContext bool
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
// larger than the byte budget runs alone. Zero declares no byte cost.
func (s *Store) WriteWeighted(ctx context.Context, bytes int, fn func(context.Context, Tx) error) error {
	return s.WriteWith(ctx, WriteOptions{Bytes: bytes}, fn)
}

// WriteWith is Write with a priority, durability and byte cost.
func (s *Store) WriteWith(ctx context.Context, opts WriteOptions, fn func(context.Context, Tx) error) error {
	if opts.Bytes < 0 || opts.Work < 0 || fn == nil || opts.Priority >= priorities || opts.Durability > Full || opts.BindCallerContext {
		return errors.New("SQLite write callback, priority, durability and nonnegative byte and work costs required; BindCallerContext uses WriteTx or WriteGroupedTx")
	}
	return s.submit(s.newJob(ctx, opts, func(j *job) { j.write = fn }))
}

// WriteTx runs fn in a database/sql transaction of its own on the writer,
// for code written against *sql.Tx. The transaction begins IMMEDIATE (see
// Options.DSNParams for drivers other than "sqlite" and "sqlite3"), follows
// the caller's context for callback SQL and retry waits, and commits when
// fn returns nil. BindCallerContext also binds BEGIN, the transaction
// lifetime and COMMIT. Bytes, Work and Interruptible in opts are ignored.
// fn must not commit or roll back tx; use its context for AfterCommit.
func (s *Store) WriteTx(ctx context.Context, opts WriteOptions, fn func(context.Context, *sql.Tx) error) error {
	if fn == nil || opts.Priority >= priorities || opts.Durability > Full {
		return errors.New("SQLite transaction callback, priority and durability required")
	}
	return s.submit(s.newJob(ctx, opts, func(j *job) { j.tx = fn }))
}

// WriteGroupedTx is WriteWith for callbacks that require a *sql.Tx, such as
// adapters using their own nested savepoints. Native callbacks batch with one
// another and share the same transaction. Nested savepoint names must not use
// the sdk_ prefix; finish all work before returning and never commit or roll
// back the shared transaction. SQL must use the supplied context, which keeps
// cancellation local to each callback's savepoint. The Store checks the
// caller's cancellation before and after the callback. With
// BindCallerContext the callback runs alone with WriteTx semantics.
func (s *Store) WriteGroupedTx(ctx context.Context, opts WriteOptions, fn func(context.Context, *sql.Tx) error) error {
	if fn == nil || opts.Bytes < 0 || opts.Work < 0 || opts.Priority >= priorities || opts.Durability > Full || opts.Interruptible {
		return errors.New("SQLite grouped transaction callback, priority, durability and nonnegative byte and work costs required; interruptible writes use WriteTx or BindCallerContext")
	}
	return s.submit(s.newJob(ctx, opts, func(j *job) { j.tx, j.groupTx = fn, !opts.BindCallerContext }))
}

func (s *Store) newJob(ctx context.Context, opts WriteOptions, set func(*job)) *job {
	j := &job{ctx: ctx, priority: opts.Priority, durability: max(opts.Durability, s.opts.Durability),
		bytes: opts.Bytes, work: opts.Work, interruptible: opts.Interruptible,
		bindCallerContext: opts.BindCallerContext, observe: opts.Observe}
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
	groupTx   bool
	exclusive func(context.Context, *sql.Conn) error
	// interruptible runs alone and passes the caller's context to its SQL.
	interruptible     bool
	bindCallerContext bool
	priority          Priority
	durability        Durability
	bytes, work       int
	observe           func(WriteStats)
	queuedAt          time.Time
	enqueuedTurn      uint64
	hooks             []func() // Owned by the writer goroutine.
	started           bool     // Protected by writeQueue.mu.
	done              chan result
}

// grouped reports whether the job may share a transaction.
func (j *job) grouped() bool { return j.groupTx || (j.write != nil && !j.interruptible) }

type result struct {
	err        error
	panicValue any
	stats      *WriteStats
}

type writeQueue struct {
	mu      sync.Mutex
	items   [priorities][]*job
	closing bool
	turn    uint64 // Writer dispatches, regardless of duration or batch size.
	// interactiveLast records which of Interactive and Background ran last.
	interactiveLast bool
	changed         chan struct{} // Closed and replaced whenever the queue changes.
	// arriving counts callers inside enqueue that have not queued yet and
	// are not waiting for queue space. An idle writer lets them join.
	arriving atomic.Int64
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

// size counts the waiting operations. Hold q.mu.
func (q *writeQueue) size() int {
	n := 0
	for _, items := range q.items {
		n += len(items)
	}
	return n
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
	counts := s.PendingByPriority()
	return counts[Interactive] + counts[Urgent] + counts[Background]
}

// PendingByPriority reports a single snapshot of admitted waiting operations,
// indexed by Priority. Aged work is counted under its original priority.
func (s *Store) PendingByPriority() [priorities]int {
	q := &s.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	var counts [priorities]int
	for p, items := range q.items {
		counts[p] = len(items)
	}
	return counts
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
	q.arriving.Add(1)
	arriving := true
	leave := func() {
		if arriving {
			q.arriving.Add(-1)
			arriving = false
		}
	}
	defer leave()
	for {
		if !arriving {
			q.arriving.Add(1)
			arriving = true
		}
		if err := j.ctx.Err(); err != nil {
			return err
		}
		q.mu.Lock()
		if q.closing {
			q.mu.Unlock()
			return ErrClosed
		}
		if len(q.items[j.priority]) < s.opts.QueueSize {
			j.enqueuedTurn = q.turn
			q.items[j.priority] = append(q.items[j.priority], j)
			leave()
			q.signal()
			q.mu.Unlock()
			return nil
		}
		changed := q.changed
		// Waiting for space is not arriving: the writer must not wait for it.
		leave()
		q.mu.Unlock()
		select {
		case <-changed:
		case <-j.ctx.Done():
			return j.ctx.Err()
		}
	}
}

// Age lower priorities after eight writer dispatches. Choose the oldest
// waiting head when both have aged; ties fall through to normal ordering.
const agingTurns = 8

func (q *writeQueue) next() Priority {
	var aged Priority
	var age uint64
	for _, p := range [...]Priority{Urgent, Interactive, Background} {
		if items := q.items[p]; len(items) != 0 {
			wait := q.turn - items[0].enqueuedTurn
			if wait >= agingTurns && (wait > age || (wait == age && aged == Interactive && p == Background && q.interactiveLast)) {
				aged, age = p, wait
			}
		}
	}
	if age != 0 {
		if aged != Urgent {
			q.interactiveLast = aged == Interactive
		}
		return aged
	}
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
	woke := false
	for {
		q.mu.Lock()
		if woke && !q.empty() {
			// An idle writer was woken by the first of possibly several
			// concurrent callers. Yield so callers already running can queue
			// before the group forms, and keep yielding while they arrive, so
			// a simultaneous burst shares one commit. Bounded by the item
			// limit, never by a clock: a lone caller costs one yield. Callers
			// the Go scheduler has not run yet can still form the next group.
			for range s.opts.MaxBatchItems {
				queued := q.size()
				q.mu.Unlock()
				runtime.Gosched()
				q.mu.Lock()
				if q.arriving.Load() == 0 && q.size() == queued {
					break
				}
			}
		}
		if !q.empty() {
			p := q.next()
			q.turn++
			items := q.items[p]
			first := items[0]
			batch, rest := []*job{first}, items[1:]
			workLimit := s.opts.MaxBatchWork
			if first.grouped() && (workLimit == 0 || (first.work > 0 && first.work < workLimit)) {
				rest = []*job{}
				bytesLeft := s.opts.MaxBatchBytes - first.bytes
				workLeft := workLimit - first.work
				for i, j := range items[1:] {
					if !j.grouped() || j.groupTx != first.groupTx || len(batch) == s.opts.MaxBatchItems ||
						(j.durability == first.durability && (j.bytes > bytesLeft ||
							(workLimit > 0 && (j.work == 0 || j.work > workLeft)))) {
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
					bytesLeft -= j.bytes
					if workLimit > 0 {
						workLeft -= j.work
					}
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
		woke = true
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
		err = s.withTurn(context.Background(), func() error { return s.commit(batch, results, timing) })
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
	conn := s.writer
	defer func() {
		// Leave the connection as the next group expects it: outside a
		// transaction (an error here means none was open), with the Store's
		// own settings.
		ctx := context.WithoutCancel(j.ctx)
		if errors.Is(err, sql.ErrConnDone) {
			s.resetWriter()
			return
		}
		if rollbackErr := s.rollback(ctx); rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
			return
		}
		restoreErr := s.applyWriterPragmas(ctx, conn, nil)
		if restoreErr == nil {
			restoreErr = s.configureCheckpoints(ctx, conn)
		}
		if restoreErr != nil {
			s.resetWriter()
			err = errors.Join(err, restoreErr)
		}
	}()
	return j.exclusive(context.WithValue(j.ctx, operationKey{}, s), conn)
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
	waitCtx, stop := batchContext(batch)
	defer stop()
	if waitCtx.Err() != nil {
		return context.Cause(waitCtx)
	}
	ctx := context.WithoutCancel(waitCtx)
	first := batch[0]
	sqlCtx := ctx
	if first.bindCallerContext {
		sqlCtx = first.ctx
		// database/sql can discard the connection on automatic rollback.
		// Close the Store's prepared statements while the driver is live.
		s.statements.reset()
	}
	if err := s.ensureWriter(sqlCtx); err != nil {
		return err
	}
	if err := s.setSync(sqlCtx, first.durability); err != nil {
		if first.bindCallerContext && sqlCtx.Err() != nil && s.writer != nil {
			// The pragma may have applied before the driver saw cancellation.
			s.resetWriter()
		}
		return err
	}

	start := time.Now()
	var native *sql.Tx
	if err := s.retryBusy(waitCtx, "BEGIN IMMEDIATE", func() error {
		var err error
		if first.tx != nil {
			native, err = s.writer.BeginTx(sqlCtx, nil)
		} else {
			_, err = s.writer.ExecContext(ctx, "BEGIN IMMEDIATE")
		}
		return err
	}); err != nil {
		if errors.Is(err, sql.ErrConnDone) || (first.bindCallerContext && sqlCtx.Err() != nil) {
			s.resetWriter()
		}
		return fmt.Errorf("begin SQLite write: %w", withCancellation(sqlCtx, err))
	}
	if timing != nil {
		timing.begin = time.Since(start)
		start = time.Now()
	}
	committed := false
	defer func() {
		if native != nil {
			// Release database/sql's transaction and prepared statements.
			// After a successful SQL COMMIT, SQLite has nothing to roll back.
			rollbackErr := native.Rollback()
			if first.bindCallerContext && errors.Is(rollbackErr, sql.ErrTxDone) {
				// database/sql may still be rolling back asynchronously.
				// Close waits for the transaction and this writer pool has
				// no idle connections. Do not start another SQL transaction
				// on a connection that can still receive that rollback.
				_ = s.writer.Close()
				s.writer = nil
				return
			}
			if committed && rollbackErr != nil &&
				!errors.Is(rollbackErr, sql.ErrTxDone) && !noTransaction(rollbackErr) {
				// The commit succeeded. Keep that outcome and replace the
				// connection before serving the next group.
				s.resetWriter()
			}
		}
		if !committed {
			err = errors.Join(err, s.rollback(ctx))
		}
	}()
	executor := Tx(s.writer)
	if native != nil {
		executor = native
	}

	successful := make([]*job, 0, len(batch))
	for i, j := range batch {
		if err := j.ctx.Err(); err != nil {
			results[i].err = err
			continue
		}
		if j.grouped() {
			r, err := s.runItem(ctx, executor, native, j)
			results[i] = r
			if err != nil {
				return err
			}
		} else {
			results[i] = s.callItem(j, native, s.callbackContext(j))
		}
		if results[i].err == nil && results[i].panicValue == nil {
			successful = append(successful, j)
		}
	}
	if timing != nil {
		timing.work = time.Since(start)
	}
	if len(successful) == 0 {
		return nil
	}

	commitCtx, stopCommit := batchContext(successful)
	defer stopCommit()
	if timing != nil {
		timing.commitStarted = time.Now()
	}
	// Use SQL COMMIT while the transaction is still live. database/sql
	// marks a *sql.Tx done on the first Commit call, and SQLite drivers can
	// roll the transaction back if that call returns SQLITE_BUSY.
	err = s.retryBusy(commitCtx, "COMMIT", func() error {
		_, err := executor.ExecContext(sqlCtx, "COMMIT")
		return err
	})
	if timing != nil {
		timing.commitFinished = time.Now()
		timing.commit = timing.commitFinished.Sub(timing.commitStarted)
	}
	if err != nil {
		return fmt.Errorf("commit SQLite write: %w", withCancellation(sqlCtx, err))
	}
	committed = true
	return nil
}

// rollback ends the transaction on the writer. When SQLite already rolled
// it back (after an interrupt), there is nothing left to do.
func (s *Store) rollback(ctx context.Context) error {
	if s.writer == nil {
		return nil
	}
	if _, err := s.writer.ExecContext(ctx, "ROLLBACK"); err != nil && !noTransaction(err) {
		s.resetWriter()
		return fmt.Errorf("roll back SQLite group: %w", err)
	}
	return nil
}

func noTransaction(err error) bool {
	return strings.Contains(err.Error(), "no transaction is active")
}

func (s *Store) callbackContext(j *job) context.Context {
	return context.WithValue(context.WithValue(j.ctx, operationKey{}, s), itemKey{}, j)
}

func withCancellation(ctx context.Context, err error) error {
	if canceled := ctx.Err(); canceled != nil && !errors.Is(err, canceled) {
		return errors.Join(err, canceled)
	}
	return err
}

// batchContext ends only when every caller still participating in this
// phase has canceled. A single caller's cancellation cannot abort a peer.
func batchContext(batch []*job) (context.Context, func()) {
	if len(batch) == 1 {
		return batch[0].ctx, func() {}
	}
	base := context.WithoutCancel(batch[0].ctx)
	for _, j := range batch {
		if j.ctx.Done() == nil {
			return base, func() {}
		}
	}
	ctx, cancel := context.WithCancelCause(base)
	var remaining atomic.Int64
	remaining.Store(int64(len(batch)))
	stops := make([]func() bool, 0, len(batch))
	for _, j := range batch {
		if j.ctx.Err() != nil {
			if remaining.Add(-1) == 0 {
				cancel(j.ctx.Err())
			}
			continue
		}
		stops = append(stops, context.AfterFunc(j.ctx, func() {
			if remaining.Add(-1) == 0 {
				cancel(j.ctx.Err())
			}
		}))
	}
	return ctx, func() {
		for _, stop := range stops {
			stop()
		}
		cancel(nil)
	}
}

// busyRetryDelay spaces attempts; it is not a failure budget.
const busyRetryDelay = 25 * time.Millisecond

// BusyAttempt describes a BEGIN IMMEDIATE or COMMIT that found the database
// locked by another connection after the busy timeout.
type BusyAttempt struct {
	Statement string // "BEGIN IMMEDIATE" or "COMMIT"
	Attempt   int    // 1 for the first busy result
	Err       error
	// Context is the caller's context for a single write. For a group, it
	// carries the first participating caller's values and becomes done
	// when all still-participating callers cancel.
	Context context.Context
}

// retryBusy repeats a statement that failed with SQLITE_BUSY. BEGIN
// IMMEDIATE has done nothing yet; a busy COMMIT keeps its transaction open.
func (s *Store) retryBusy(ctx context.Context, name string, statement func() error) error {
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		err := statement()
		if !isBusy(err) {
			return err
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		delay := busyRetryDelay
		if s.opts.BusyRetry != nil {
			var retry bool
			if delay, retry = s.opts.BusyRetry(BusyAttempt{Statement: name, Attempt: attempt, Err: err, Context: ctx}); !retry {
				return err
			}
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return context.Cause(ctx)
			}
		}
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

func (s *Store) runItem(ctx context.Context, executor Tx, native *sql.Tx, j *job) (result, error) {
	if _, err := executor.ExecContext(ctx, "SAVEPOINT sdk_item"); err != nil {
		return result{}, fmt.Errorf("start SQLite write savepoint: %w", err)
	}
	r := s.callItem(j, native, context.WithoutCancel(s.callbackContext(j)))
	if r.err != nil || r.panicValue != nil {
		if _, err := executor.ExecContext(ctx, "ROLLBACK TO sdk_item"); err != nil {
			return r, fmt.Errorf("roll back SQLite write savepoint: %w", err)
		}
	}
	if _, err := executor.ExecContext(ctx, "RELEASE sdk_item"); err != nil {
		return r, fmt.Errorf("release SQLite write savepoint: %w", err)
	}
	return r, nil
}

func (s *Store) callItem(j *job, native *sql.Tx, sqlCtx context.Context) (r result) {
	defer func() {
		r.panicValue = recover()
		if r.err != nil || r.panicValue != nil {
			j.hooks = nil
		}
	}()
	if native != nil {
		r.err = j.tx(sqlCtx, native)
	} else {
		r.err = j.write(s.callbackContext(j), itemTx{s, sqlCtx})
	}
	if r.err == nil {
		r.err = j.ctx.Err()
	} else if j.bindCallerContext {
		r.err = withCancellation(j.ctx, r.err)
	}
	return r
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
