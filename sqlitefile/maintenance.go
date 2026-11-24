package sqlitefile

import (
	"context"
	"database/sql"
	"fmt"
)

// Configure sets a 64 MiB retained journal size limit and auto-checkpoint
// threshold of 1000 pages. Apply it on every physical writer connection,
// outside transactions. It leaves the App's journal and synchronous modes alone.
func Configure(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "PRAGMA main.journal_size_limit=67108864"); err != nil {
		return fmt.Errorf("set SQLite journal size limit: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA wal_autocheckpoint=1000"); err != nil {
		return fmt.Errorf("set SQLite WAL autocheckpoint: %w", err)
	}
	return nil
}

// Maintain runs PRAGMA optimize and, in incremental auto-vacuum mode, reclaims
// up to 1024 pages if at least one fifth of the main database is on the freelist.
// Call it from a serialized App-owned schedule, ideally during a quiet period.
func Maintain(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA main.optimize"); err != nil {
		return fmt.Errorf("optimize SQLite database: %w", err)
	}

	var mode, pages, freePages int64
	if err := conn.QueryRowContext(ctx, "PRAGMA main.auto_vacuum").Scan(&mode); err != nil {
		return fmt.Errorf("read SQLite auto-vacuum mode: %w", err)
	}
	if mode != 2 {
		return nil
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA main.page_count").Scan(&pages); err != nil {
		return fmt.Errorf("read SQLite page count: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA main.freelist_count").Scan(&freePages); err != nil {
		return fmt.Errorf("read SQLite freelist count: %w", err)
	}
	if pages == 0 || freePages == 0 || freePages*5 < pages {
		return nil
	}
	rows, err := conn.QueryContext(ctx, "PRAGMA main.incremental_vacuum(1024)")
	if err != nil {
		return fmt.Errorf("start SQLite incremental vacuum: %w", err)
	}
	defer rows.Close()
	// SQLite yields a result row for each iteration. Step through them all
	// so drivers that stop at the first result do not truncate the work.
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("finish SQLite incremental vacuum: %w", err)
	}
	return nil
}
