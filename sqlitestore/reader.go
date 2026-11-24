package sqlitestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
)

func openReader(ctx context.Context, d driver.Driver, dsn string, limit, overflow int, pragmas []string) (*sql.DB, error) {
	var base driver.Connector = namedConnector{driver: d, dsn: dsn}
	if dc, ok := d.(driver.DriverContext); ok {
		var err error
		base, err = dc.OpenConnector(dsn)
		if err != nil {
			return nil, err
		}
	}
	db := sql.OpenDB(readerConnector{Connector: base, pragmas: pragmas})
	db.SetMaxOpenConns(limit + overflow)
	db.SetMaxIdleConns(limit)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Initialize every physical connection, including replacements created by the
// pool. Return the driver's connection unchanged so its optional interfaces
// remain available to database/sql.
type readerConnector struct {
	driver.Connector
	pragmas []string
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
	return conn, nil
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
