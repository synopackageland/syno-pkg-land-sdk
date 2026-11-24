// Package sqlitestore manages a file-backed SQLite store with one serialized
// writer and separate read-only database/sql pools.
package sqlitestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"

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

	BusyTimeoutMS      int // Default 5000. Use -1 to disable SQLite lock waiting.
	Readers            int // Default 2.
	BackgroundReaders  int // Default 0, which leaves BackgroundReader nil.
	WriterCacheKiB     int // Default 2048.
	ReaderCacheKiB     int // Default 512 per connection.
	BackgroundCacheKiB int // Default 1024 per connection.

	QueueSize     int // Default 128 waiting operations.
	MaxBatchItems int // Default 32 callbacks per transaction.
	MaxBatchBytes int // Default 1 MiB of declared weight per transaction.

	DisableForeignKeys bool // Foreign keys are enabled by default.
	TempStoreMemory    bool // SQLite temporary storage defaults to FILE.
	AllowTrustedSchema bool // SQLite trusted_schema defaults to OFF.
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
		{&o.Readers, 2}, {&o.BackgroundReaders, 0}, {&o.WriterCacheKiB, 2048},
		{&o.ReaderCacheKiB, 512}, {&o.BackgroundCacheKiB, 1024}, {&o.QueueSize, 128},
		{&o.MaxBatchItems, 32}, {&o.MaxBatchBytes, 1 << 20},
	} {
		if *option.value < 0 {
			return errors.New("SQLite pool, cache and queue limits must be nonnegative")
		}
		if *option.value == 0 {
			*option.value = option.fallback
		}
	}
	if max(o.WriterCacheKiB, o.ReaderCacheKiB, o.BackgroundCacheKiB) > 1<<31-1 {
		return errors.New("SQLite cache size exceeds the supported limit")
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
	writerDB *sql.DB
	writer   *sql.Conn // Owned by the writer goroutine after Open.
	queue    writeQueue
	done     chan struct{}
	close    sync.Once
	closeErr error
}

// Open opens or creates a SQLite database in a dedicated, App-owned directory.
// It prepares the directory, configures WAL and validates the read pools
// before starting the writer. The App owns schema migrations.
func Open(ctx context.Context, path string, opts Options) (_ *Store, err error) {
	if err := opts.defaults(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("SQLite database path is required")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Reject unknown drivers before touching the filesystem.
	writerDB, err := sql.Open(opts.DriverName, fileURI(path, "rw"))
	if err != nil {
		return nil, err
	}
	writerDB.SetMaxOpenConns(1)
	writerDB.SetMaxIdleConns(1)
	s := &Store{opts: opts, writerDB: writerDB, done: make(chan struct{}),
		queue: writeQueue{changed: make(chan struct{})}}
	defer func() {
		if err != nil {
			_ = s.closeHandles()
		}
	}()
	if err := sqlitefile.PrepareDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := prepareDatabase(path); err != nil {
		return nil, fmt.Errorf("prepare SQLite database: %w", err)
	}
	if err := s.ensureWriter(ctx); err != nil {
		return nil, err
	}
	s.Reader, err = openReader(ctx, writerDB.Driver(), path, opts.Readers, opts.pragmas(opts.ReaderCacheKiB, true))
	if err != nil {
		return nil, fmt.Errorf("open SQLite reader: %w", err)
	}
	if opts.BackgroundReaders > 0 {
		s.BackgroundReader, err = openReader(ctx, writerDB.Driver(), path, opts.BackgroundReaders,
			opts.pragmas(opts.BackgroundCacheKiB, true))
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

func fileURI(path, mode string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=" + mode}
	return u.String()
}

func (o Options) pragmas(cacheKiB int, readOnly bool) []string {
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
	queries := []string{
		fmt.Sprintf("PRAGMA busy_timeout=%d", o.BusyTimeoutMS),
		"PRAGMA foreign_keys=" + foreignKeys,
		"PRAGMA temp_store=" + tempStore,
		"PRAGMA trusted_schema=" + trustedSchema,
		fmt.Sprintf("PRAGMA main.cache_size=-%d", cacheKiB),
	}
	if readOnly {
		return append(queries, "PRAGMA query_only=ON")
	}
	if o.Durability == Full {
		return append(queries, "PRAGMA synchronous=FULL")
	}
	return append(queries, "PRAGMA synchronous=NORMAL")
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
	s.writer = conn
	return nil
}

func (s *Store) configureWriter(ctx context.Context, conn *sql.Conn) error {
	for _, query := range s.opts.pragmas(s.opts.WriterCacheKiB, false) {
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("configure SQLite writer (%s): %w", query, err)
		}
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
	return sqlitefile.Configure(ctx, conn)
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

func (s *Store) closeHandles() error {
	var errs []error
	for _, db := range []*sql.DB{s.BackgroundReader, s.Reader} {
		if db != nil {
			errs = append(errs, db.Close())
		}
	}
	if s.writer != nil {
		err := s.writer.Close()
		if !errors.Is(err, sql.ErrConnDone) {
			errs = append(errs, err)
		}
	}
	return errors.Join(append(errs, s.writerDB.Close())...)
}
