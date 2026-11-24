// Package sqlitestore manages a file-backed SQLite store with one serialized
// writer and separate read-only database/sql pools.
package sqlitestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitefile"
)

// Durability selects the writer's SQLite synchronous mode in WAL mode.
type Durability uint8

const (
	Normal Durability = iota // The zero value uses synchronous=NORMAL.
	Full                     // Sync the WAL at every successful commit.
)

const (
	defaultBusyTimeoutMS = 5000
	noBusyWait           = 0
)

// Options configures Open. DriverName is the name of a registered SQLite
// driver that supports SQLite file URIs. Other zero values use the defaults.
type Options struct {
	DriverName string
	Durability Durability
	MustExist  bool // Open an existing database without creating the file or its directory.

	BusyTimeoutMS int // Default 5000. Use -1 to disable SQLite lock waiting.
	Readers       int // Default 2.
	// OverflowReaders lets Reader open this many more connections under
	// load. They close when released while Readers connections are idle.
	OverflowReaders    int
	BackgroundReaders  int // Default 0, which leaves BackgroundReader nil.
	WriterCacheKiB     int // Default 2048.
	ReaderCacheKiB     int // Default 512 per connection.
	OverflowCacheKiB   int // Default ReaderCacheKiB; a distinct cache uses disposable overflow connections.
	BackgroundCacheKiB int // Default 1024 per connection.

	QueueSize     int // Default 128 waiting operations per Priority.
	MaxBatchItems int // Default 32 callbacks per transaction.
	MaxBatchBytes int // Default 1 MiB of declared weight per transaction.

	DisableForeignKeys bool // Foreign keys are enabled by default.
	TempStoreMemory    bool // SQLite temporary storage defaults to FILE.
	AllowTrustedSchema bool // SQLite trusted_schema defaults to OFF.
	// JournalSizeLimit is the WAL size kept after a checkpoint, in bytes.
	// Zero uses the sqlitefile default of 64 MiB.
	JournalSizeLimit int64
	// AutoCheckpointPages sets the number of WAL pages that triggers an
	// automatic checkpoint. Zero uses the sqlitefile default of 1000 pages.
	AutoCheckpointPages int

	// DSNParams are added to every connection's file URI, for driver
	// options such as modernc's _txlock. The Store sets mode itself; with
	// driver "sqlite" or "sqlite3" it also sets _txlock=immediate on the
	// writer for WriteTx.
	DSNParams url.Values
	// ConnectionInit runs once per physical connection, after the Store's
	// connection pragmas and before query_only on readers: for example
	// ATTACH of another file (add ?mode=ro for a read-only attachment) or
	// App pragmas. Statements take no arguments.
	ConnectionInit []string
	// BusyRetry decides what happens when BEGIN IMMEDIATE or COMMIT finds
	// the database locked after the busy timeout: wait delay and retry, or
	// give up with the busy error. By default the writer retries every
	// 25 ms. A group stops retrying when its remaining callers cancel.
	BusyRetry func(BusyAttempt) (delay time.Duration, retry bool)
	// Turn, when shared by several Stores, makes their writes take turns.
	Turn *Turn

	// ConfigureWriter, when set, runs on every new physical writer
	// connection after the Store's settings and before its first
	// transaction, including a connection that replaces a failed one. Use it
	// for per-connection state such as TEMP triggers or functions.
	ConfigureWriter func(context.Context, *sql.Conn) error
	// Observe, when set, receives the WriteStats of each write that reached
	// a transaction, on the caller's goroutine after its outcome. It must
	// not block; a panic in it is ignored.
	Observe func(WriteStats)
}

func (o *Options) defaults() error {
	if o.DriverName == "" || o.Durability > Full || o.BusyTimeoutMS < -1 || o.BusyTimeoutMS > 1<<31-1 {
		return errors.New("invalid SQLite driver, durability or busy timeout")
	}
	if o.BusyTimeoutMS == -1 {
		o.BusyTimeoutMS = noBusyWait
	} else if o.BusyTimeoutMS == 0 {
		o.BusyTimeoutMS = defaultBusyTimeoutMS
	}
	for _, option := range []struct {
		value    *int
		fallback int
	}{
		{&o.Readers, 2}, {&o.OverflowReaders, 0}, {&o.BackgroundReaders, 0}, {&o.WriterCacheKiB, 2048},
		{&o.ReaderCacheKiB, 512}, {&o.OverflowCacheKiB, 0}, {&o.BackgroundCacheKiB, 1024}, {&o.QueueSize, 128},
		{&o.MaxBatchItems, 32}, {&o.MaxBatchBytes, 1 << 20},
	} {
		if *option.value < 0 {
			return errors.New("SQLite pool, cache and queue limits must be nonnegative")
		}
		if *option.value == 0 {
			*option.value = option.fallback
		}
	}
	if max(o.WriterCacheKiB, o.ReaderCacheKiB, o.OverflowCacheKiB, o.BackgroundCacheKiB) > math.MaxInt32 {
		return errors.New("SQLite cache size exceeds the supported limit")
	}
	if o.OverflowReaders > math.MaxInt-o.Readers {
		return errors.New("SQLite reader pool size exceeds the supported limit")
	}
	if o.JournalSizeLimit < 0 || o.AutoCheckpointPages < 0 || o.AutoCheckpointPages > math.MaxInt32 || o.DSNParams.Has("mode") {
		return errors.New("invalid SQLite journal limits or DSNParams mode override")
	}
	return nil
}

// Store owns an interactive read pool, an optional background read pool
// and a single write queue. Close releases
// all handles. A Store must be constructed by Open and must never be copied.
type Store struct {
	// Reader is the interactive read-only pool. BackgroundReader is a separate
	// read-only pool when configured. The Store owns both pools.
	Reader           *sql.DB
	BackgroundReader *sql.DB

	opts     Options
	path     string
	writerDB *sql.DB
	// The writer goroutine owns these after Open.
	writer     *sql.Conn
	sync       Durability // The writer connection's current sync mode.
	statements statementCache
	queue      writeQueue
	readers    struct {
		sync.Mutex
		extra  []*sql.DB
		closed bool
	}
	done     chan struct{}
	close    sync.Once
	closeErr error
}

// Open opens or creates a SQLite database in a dedicated, App-owned directory.
// It prepares the directory and configures the writer. Reader pools open
// physical connections on first use, so the App can migrate first.
func Open(ctx context.Context, path string, opts Options) (_ *Store, err error) {
	if err := opts.defaults(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts.ConnectionInit = slices.Clone(opts.ConnectionInit)
	params := make(url.Values, len(opts.DSNParams))
	for key, values := range opts.DSNParams {
		params[key] = slices.Clone(values)
	}
	opts.DSNParams = params
	if path == "" {
		return nil, errors.New("SQLite database path is required")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	writerParams := url.Values{}
	if opts.DriverName == "sqlite" || opts.DriverName == "sqlite3" {
		writerParams.Set("_txlock", "immediate")
	}
	// Reject unknown drivers before touching the filesystem.
	writerDB, err := sql.Open(opts.DriverName, opts.fileURI(path, "rw", writerParams))
	if err != nil {
		return nil, err
	}
	writerDB.SetMaxOpenConns(1)
	// The Store pins the writer until it is discarded. Keep no idle
	// connection that could return to ensureWriter already initialized.
	writerDB.SetMaxIdleConns(0)
	s := &Store{opts: opts, path: path, writerDB: writerDB, done: make(chan struct{}),
		queue: writeQueue{changed: make(chan struct{})}}
	defer func() {
		if err != nil {
			_ = s.closeHandles()
		}
	}()
	if !opts.MustExist {
		if err := prepareDir(filepath.Dir(path)); err != nil {
			return nil, err
		}
		if err := prepareDatabase(path); err != nil {
			return nil, fmt.Errorf("prepare SQLite database: %w", err)
		}
	}
	if err := s.ensureWriter(ctx); err != nil {
		return nil, err
	}
	s.Reader, err = s.newReader(opts.Readers, opts.OverflowReaders, opts.ReaderCacheKiB, opts.OverflowCacheKiB)
	if err != nil {
		return nil, fmt.Errorf("open SQLite reader: %w", err)
	}
	if opts.BackgroundReaders > 0 {
		s.BackgroundReader, err = s.newReader(opts.BackgroundReaders, 0, opts.BackgroundCacheKiB, 0)
		if err != nil {
			return nil, fmt.Errorf("open SQLite background reader: %w", err)
		}
	}
	go s.run()
	return s, nil
}

func prepareDatabase(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		return file.Close()
	}
	if !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("SQLite database must be a regular file")
	}
	return nil
}

// prepareDir prepares the database directory when Open creates it. New files
// and subdirectories inherit its NoCOW flag, so an existing directory needs
// no preparation.
func prepareDir(dir string) error {
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return sqlitefile.PrepareDir(dir)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("SQLite database directory is not a directory")
	}
	return nil
}

func (o Options) fileURI(path, mode string, extra url.Values) string {
	q := url.Values{}
	for _, params := range []url.Values{o.DSNParams, extra} {
		for key, values := range params {
			q[key] = append([]string(nil), values...)
		}
	}
	q.Set("mode", mode)
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: q.Encode()}
	return u.String()
}

func (o Options) pragmas(cacheKiB int) []string {
	foreignKeys, tempStore, trustedSchema := "ON", "FILE", "OFF"
	if o.DisableForeignKeys {
		foreignKeys = "OFF"
	}
	if o.TempStoreMemory {
		tempStore = "MEMORY"
	}
	if o.AllowTrustedSchema {
		trustedSchema = "ON"
	}
	return []string{
		fmt.Sprintf("PRAGMA busy_timeout=%d", o.BusyTimeoutMS),
		"PRAGMA foreign_keys=" + foreignKeys,
		"PRAGMA temp_store=" + tempStore,
		"PRAGMA trusted_schema=" + trustedSchema,
		fmt.Sprintf("PRAGMA main.cache_size=-%d", cacheKiB),
	}
}

func syncPragma(d Durability) string {
	if d == Full {
		return "PRAGMA synchronous=FULL"
	}
	return "PRAGMA synchronous=NORMAL"
}

func (s *Store) ensureWriter(ctx context.Context) error {
	if s.writer != nil {
		return nil
	}
	conn, err := s.writerDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open SQLite writer: %w", err)
	}
	if err := s.configureWriter(ctx, conn); err != nil {
		discard(conn)
		return err
	}
	if s.opts.ConfigureWriter != nil {
		if err := s.opts.ConfigureWriter(ctx, conn); err != nil {
			discard(conn)
			return fmt.Errorf("configure SQLite writer: %w", err)
		}
	}
	s.writer = conn
	return nil
}

// applyWriterPragmas sets the Store's connection settings and its default
// sync mode on the writer.
func (s *Store) applyWriterPragmas(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only=OFF"); err != nil {
		return fmt.Errorf("restore SQLite writer query_only: %w", err)
	}
	for _, query := range append(s.opts.pragmas(s.opts.WriterCacheKiB), syncPragma(s.opts.Durability)) {
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("configure SQLite writer (%s): %w", query, err)
		}
	}
	s.sync = s.opts.Durability
	return sqlitefile.ConfigureWith(ctx, conn, sqlitefile.ConfigureOptions{
		JournalSizeLimit: s.opts.JournalSizeLimit, AutoCheckpointPages: s.opts.AutoCheckpointPages,
	})
}

func (s *Store) configureWriter(ctx context.Context, conn *sql.Conn) error {
	if err := s.applyWriterPragmas(ctx, conn); err != nil {
		return err
	}
	var mode string
	if err := conn.QueryRowContext(ctx, "PRAGMA main.journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("read SQLite journal mode: %w", err)
	}
	if mode != "wal" {
		if err := conn.QueryRowContext(ctx, "PRAGMA main.journal_mode=WAL").Scan(&mode); err != nil {
			return fmt.Errorf("enable SQLite WAL: %w", err)
		}
		if mode != "wal" {
			return fmt.Errorf("SQLite refused WAL mode (got %q)", mode)
		}
	}
	for _, query := range s.opts.ConnectionInit {
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("initialize SQLite writer (%s): %w", query, err)
		}
	}
	return nil
}

func discard(conn *sql.Conn) {
	// A failed rollback leaves the SQL state of this connection unknown.
	// Returning ErrBadConn from Raw makes database/sql discard it.
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

// Close stops admitting new writes and maintenance, drains admitted operations
// and closes the database handles. It is safe to call concurrently.
func (s *Store) Close() error {
	s.close.Do(func() {
		s.queue.stop()
		<-s.done
		s.closeErr = s.closeHandles()
	})
	return s.closeErr
}

// ReaderOptions configures an additional read pool.
type ReaderOptions struct {
	Conns    int // Default 1.
	CacheKiB int // Default ReaderCacheKiB per connection.
}

// OpenReader opens another read-only pool with the settings of Reader, for
// a workload that must not take the interactive or background pools'
// connections. The Store owns the pool and closes it in Close.
func (s *Store) OpenReader(ctx context.Context, opts ReaderOptions) (*sql.DB, error) {
	if opts.Conns < 0 || opts.CacheKiB < 0 || opts.CacheKiB > 1<<31-1 {
		return nil, errors.New("SQLite reader limits must be nonnegative")
	}
	if opts.Conns == 0 {
		opts.Conns = 1
	}
	if opts.CacheKiB == 0 {
		opts.CacheKiB = s.opts.ReaderCacheKiB
	}
	db, err := s.newReader(opts.Conns, 0, opts.CacheKiB, 0)
	if err != nil {
		return nil, fmt.Errorf("open SQLite reader: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open SQLite reader: %w", err)
	}
	s.readers.Lock()
	defer s.readers.Unlock()
	if s.readers.closed {
		_ = db.Close()
		return nil, ErrClosed
	}
	s.readers.extra = append(s.readers.extra, db)
	return db, nil
}

func (s *Store) closeHandles() error {
	var errs []error
	s.readers.Lock()
	s.readers.closed = true
	extra := s.readers.extra
	s.readers.extra = nil
	s.readers.Unlock()
	for _, db := range append(extra, s.BackgroundReader, s.Reader) {
		if db != nil {
			errs = append(errs, db.Close())
		}
	}
	s.statements.reset()
	if s.writer != nil {
		err := s.writer.Close()
		if !errors.Is(err, sql.ErrConnDone) {
			errs = append(errs, err)
		}
	}
	return errors.Join(append(errs, s.writerDB.Close())...)
}
