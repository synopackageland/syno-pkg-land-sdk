package sqlitestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
)

func (s *Store) newReader(limit, overflow, cacheKiB, overflowCacheKiB int) (*sql.DB, error) {
	pragmas := append(s.opts.pragmas(cacheKiB), s.opts.ConnectionInit...)
	connector, err := s.readConnector(append(pragmas, "PRAGMA query_only=ON"))
	if err != nil {
		return nil, err
	}
	if overflow > 0 && overflowCacheKiB != 0 && overflowCacheKiB != cacheKiB {
		connector.tiers = &readerTiers{limit: int64(limit), overflowCache: fmt.Sprintf("PRAGMA main.cache_size=-%d", overflowCacheKiB)}
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(limit + overflow)
	db.SetMaxIdleConns(limit)
	return db, nil
}

// VacuumInto copies a committed snapshot of main to an App-controlled
// destination. It uses a dedicated connection with main opened mode=ro,
// without attachments or query_only. SQLite requires a missing or empty
// destination file. An error may leave a partial file for the caller to clean
// up. The caller owns the destination and its permissions.
func (s *Store) VacuumInto(ctx context.Context, destination string) error {
	if destination == "" {
		return errors.New("SQLite backup destination is required")
	}
	path, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	s.readers.Lock()
	closed := s.readers.closed
	s.readers.Unlock()
	if closed {
		return ErrClosed
	}
	connector, err := s.readConnector(s.opts.pragmas(s.opts.ReaderCacheKiB))
	if err != nil {
		return fmt.Errorf("open SQLite backup connection: %w", err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	if _, err := db.ExecContext(ctx, "VACUUM main INTO ?", path); err != nil {
		return fmt.Errorf("copy SQLite snapshot: %w", err)
	}
	return nil
}

func (s *Store) readConnector(pragmas []string) (readerConnector, error) {
	d := s.writerDB.Driver()
	dsn := s.opts.fileURI(s.path, "ro", nil)
	var base driver.Connector = namedConnector{driver: d, dsn: dsn}
	if dc, ok := d.(driver.DriverContext); ok {
		var err error
		base, err = dc.OpenConnector(dsn)
		if err != nil {
			return readerConnector{}, err
		}
	}
	return readerConnector{Connector: base, pragmas: pragmas}, nil
}

// Initialize every physical connection, including replacements created by the
// pool. With a shared cache size the driver's connection is returned unchanged.
type readerConnector struct {
	driver.Connector
	pragmas []string
	tiers   *readerTiers
}

func (c readerConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	for _, query := range c.pragmas {
		if err := execPragma(ctx, conn, query); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("configure SQLite reader (%s): %w", query, err)
		}
	}
	if c.tiers != nil {
		tier := c.tiers.track(conn)
		if tier.overflow {
			if err := execPragma(ctx, conn, c.tiers.overflowCache); err != nil {
				_ = tier.Close()
				return nil, fmt.Errorf("configure SQLite overflow reader cache: %w", err)
			}
		}
		conn = tier
	}
	return conn, nil
}

// Count regular physical connections until Close so overflow never occupies
// a regular slot or survives in the idle pool.
type readerTiers struct {
	regular       atomic.Int64
	limit         int64
	overflowCache string
}

func (t *readerTiers) track(conn driver.Conn) *tierConn {
	for {
		n := t.regular.Load()
		if n >= t.limit {
			return &tierConn{Conn: conn, tiers: t, overflow: true}
		}
		if t.regular.CompareAndSwap(n, n+1) {
			return &tierConn{Conn: conn, tiers: t}
		}
	}
}

type tierConn struct {
	driver.Conn
	tiers    *readerTiers
	overflow bool
}

// Unwrap returns the SQLite driver connection when using sql.Conn.Raw.
func (c *tierConn) Unwrap() driver.Conn { return c.Conn }

func (c *tierConn) Close() error {
	if !c.overflow {
		c.tiers.regular.Add(-1)
	}
	return c.Conn.Close()
}

func (c *tierConn) IsValid() bool {
	v, ok := c.Conn.(driver.Validator)
	return !c.overflow && (!ok || v.IsValid())
}

func (c *tierConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *tierConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *tierConn) CheckNamedValue(value *driver.NamedValue) error {
	if n, ok := c.Conn.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (c *tierConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, query)
	}
	stmt, err := c.Conn.Prepare(query)
	if err == nil && ctx.Err() != nil {
		_ = stmt.Close()
		return nil, ctx.Err()
	}
	return stmt, err
}

func (c *tierConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	if opts.Isolation != 0 || opts.ReadOnly {
		return nil, errors.New("SQLite driver does not support these transaction options")
	}
	tx, err := c.Conn.Begin()
	if err == nil && ctx.Err() != nil {
		_ = tx.Rollback()
		return nil, ctx.Err()
	}
	return tx, err
}

func (c *tierConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *tierConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := c.Conn.(driver.QueryerContext); ok {
		return q.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func execPragma(ctx context.Context, conn driver.Conn, query string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// These fixed statements have no arguments. Prepare and Exec are
	// available even on drivers without the optional context interfaces.
	stmt, err := conn.Prepare(query)
	if err != nil {
		return err
	}
	_, err = stmt.Exec(nil)
	return errors.Join(err, stmt.Close())
}

type namedConnector struct {
	driver driver.Driver
	dsn    string
}

func (c namedConnector) Driver() driver.Driver { return c.driver }
func (c namedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.driver.Open(c.dsn)
}
