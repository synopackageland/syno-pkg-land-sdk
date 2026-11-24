package sqlitefile

import (
	"context"
	"database/sql"
	"fmt"
)

// ConfigureOptions overrides Configure's defaults. Zero values keep them.
type ConfigureOptions struct {
	JournalSizeLimit    int64 // Bytes kept after a checkpoint; default 64 MiB.
	AutoCheckpointPages int   // WAL pages that trigger a checkpoint; default 1000.
}

// Configure sets a 64 MiB retained journal size limit and auto-checkpoint
// threshold of 1000 pages. Apply it on every physical writer connection,
// outside transactions. It leaves the App's journal and synchronous modes alone.
func Configure(ctx context.Context, conn *sql.Conn) error {
	return ConfigureWith(ctx, conn, ConfigureOptions{})
}

// ConfigureWith is Configure with App-chosen limits.
func ConfigureWith(ctx context.Context, conn *sql.Conn, opts ConfigureOptions) error {
	if opts.JournalSizeLimit < 0 || opts.AutoCheckpointPages < 0 {
		return fmt.Errorf("SQLite journal limits must be nonnegative")
	}
	if opts.JournalSizeLimit == 0 {
		opts.JournalSizeLimit = 64 << 20
	}
	if opts.AutoCheckpointPages == 0 {
		opts.AutoCheckpointPages = 1000
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA main.journal_size_limit=%d", opts.JournalSizeLimit)); err != nil {
		return fmt.Errorf("set SQLite journal size limit: %w", err)
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA wal_autocheckpoint=%d", opts.AutoCheckpointPages)); err != nil {
		return fmt.Errorf("set SQLite WAL autocheckpoint: %w", err)
	}
	return nil
}

// MaintainOptions bounds MaintainConnWith by work, never by elapsed time.
// Zero values keep Maintain's defaults.
type MaintainOptions struct {
	// OptimizeMask is the PRAGMA optimize argument, such as 0x10002.
	// Zero runs plain PRAGMA optimize.
	OptimizeMask uint32
	// AnalysisLimit bounds the rows ANALYZE examines per index during
	// optimize. Zero leaves the connection's setting.
	AnalysisLimit int
	// VacuumPages is the most pages one incremental vacuum reclaims.
	// Default 1024.
	VacuumPages int
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
	return MaintainConn(ctx, conn)
}

// MaintainConn runs the same upkeep as Maintain using a connection already
// owned by the caller. It leaves the connection open and requires exclusive
// use of the connection outside a transaction.
func MaintainConn(ctx context.Context, conn *sql.Conn) error {
	return MaintainConnWith(ctx, conn, MaintainOptions{})
}

// MaintainConnWith is MaintainConn with explicit work bounds. A changed
// analysis limit is restored before it returns.
func MaintainConnWith(ctx context.Context, conn *sql.Conn, opts MaintainOptions) error {
	if opts.AnalysisLimit < 0 || opts.VacuumPages < 0 {
		return fmt.Errorf("SQLite maintenance limits must be nonnegative")
	}
	if opts.VacuumPages == 0 {
		opts.VacuumPages = 1024
	}
	if opts.AnalysisLimit > 0 {
		var previous int
		if err := conn.QueryRowContext(ctx, "PRAGMA analysis_limit").Scan(&previous); err != nil {
			return fmt.Errorf("read SQLite analysis limit: %w", err)
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA analysis_limit=%d", opts.AnalysisLimit)); err != nil {
			return fmt.Errorf("set SQLite analysis limit: %w", err)
		}
		defer conn.ExecContext(context.WithoutCancel(ctx), fmt.Sprintf("PRAGMA analysis_limit=%d", previous))
	}
	optimize := "PRAGMA main.optimize"
	if opts.OptimizeMask != 0 {
		optimize = fmt.Sprintf("PRAGMA main.optimize=0x%x", opts.OptimizeMask)
	}
	if _, err := conn.ExecContext(ctx, optimize); err != nil {
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
	rows, err := conn.QueryContext(ctx, fmt.Sprintf("PRAGMA main.incremental_vacuum(%d)", opts.VacuumPages))
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
