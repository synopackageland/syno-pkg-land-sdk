package sqlitestore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/sqlitestore"
)

func TestExclusiveAttachmentAndLazyReaders(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	attachedPath := filepath.Join(dir, "attached.db")
	attached, err := sqlitestore.Open(ctx, attachedPath, sqlitestore.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec(ctx, attached, "CREATE TABLE items (value TEXT); INSERT INTO items VALUES ('attached')"); err != nil {
		t.Fatal(err)
	}
	if err := attached.Close(); err != nil {
		t.Fatal(err)
	}
	uri := url.URL{Scheme: "file", Path: attachedPath, RawQuery: "mode=ro"}
	configured := 0
	s, err := sqlitestore.Open(ctx, filepath.Join(dir, "main.db"), sqlitestore.Options{
		DriverName: "sqlite", BackgroundReaders: 1, AutoCheckpointPages: 5000,
		ConnectionInit:  []string{"ATTACH DATABASE '" + uri.String() + "' AS g", "PRAGMA synchronous=OFF"},
		ConfigureWriter: func(context.Context, *sql.Conn) error { configured++; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Reader.Stats().OpenConnections != 0 || s.BackgroundReader.Stats().OpenConnections != 0 {
		t.Fatal("physical reader opened before migration")
	}
	if err := s.Exclusive(ctx, func(ctx context.Context, conn *sql.Conn) error {
		if v := pragma(t, ctx, conn, "synchronous"); v != 1 {
			return fmt.Errorf("initial synchronous = %d, want NORMAL", v)
		}
		_, err := conn.ExecContext(ctx, "CREATE TABLE main.items (value TEXT); INSERT INTO main.items VALUES ('main'); PRAGMA wal_autocheckpoint=13")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Exclusive(ctx, func(ctx context.Context, conn *sql.Conn) error {
		if v := pragma(t, ctx, conn, "wal_autocheckpoint"); v != 5000 {
			return fmt.Errorf("wal_autocheckpoint = %d, want 5000", v)
		}
		var value string
		if err := conn.QueryRowContext(ctx, "SELECT value FROM g.items").Scan(&value); err != nil || value != "attached" {
			return fmt.Errorf("writer attachment = %q, %v", value, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if configured != 1 {
		t.Fatalf("writer configured %d times", configured)
	}
	for _, db := range []*sql.DB{s.Reader, s.BackgroundReader} {
		var main, attached string
		err := db.QueryRowContext(ctx, "SELECT value, (SELECT value FROM g.items) FROM items").Scan(&main, &attached)
		if err != nil || main != "main" || attached != "attached" {
			t.Fatalf("unqualified lookup = %q, attachment = %q, err = %v", main, attached, err)
		}
	}
}

func TestMustExistDoesNotCreateMissingDirectoryOrFile(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	opts := sqlitestore.Options{DriverName: "sqlite", MustExist: true}
	for _, tc := range []struct{ db, missing string }{
		{filepath.Join(dir, "missing", "state.db"), filepath.Join(dir, "missing")},
		{filepath.Join(dir, "state.db"), filepath.Join(dir, "state.db")},
	} {
		if s, err := sqlitestore.Open(ctx, tc.db, opts); err == nil {
			_ = s.Close()
			t.Fatal("MustExist opened a missing database")
		}
		if _, err := os.Stat(tc.missing); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing path %q, stat = %v", tc.missing, err)
		}
	}
	path := filepath.Join(dir, "state.db")
	s, err := sqlitestore.Open(ctx, path, sqlitestore.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec(ctx, s, "CREATE TABLE records(id INTEGER PRIMARY KEY); INSERT INTO records VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(ctx, path, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	count(t, reopened.Reader, 1)
}

func TestOverflowReaderUsesSeparateCache(t *testing.T) {
	s := openStore(t, sqlitestore.Options{Readers: 1, OverflowReaders: 1, ReaderCacheKiB: 128, OverflowCacheKiB: 16})
	ctx := t.Context()
	regular, err := s.Reader.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	for range 2 {
		overflow, err := s.Reader.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cache := pragma(t, ctx, overflow, "main.cache_size")
		queryOnly := pragma(t, ctx, overflow, "query_only")
		if err := overflow.Close(); err != nil {
			t.Fatal(err)
		}
		if cache != -16 || queryOnly != 1 {
			t.Fatalf("overflow cache %d, query_only %d", cache, queryOnly)
		}
		if got := s.Reader.Stats().OpenConnections; got != 1 {
			t.Fatalf("open while the regular connection is held = %d, want 1", got)
		}
	}
	if v := pragma(t, ctx, regular, "main.cache_size"); v != -128 {
		t.Fatalf("regular reader cache = %d", v)
	}
}

func TestVacuumIntoCopiesCommittedMainWithoutUsingReaderPool(t *testing.T) {
	ctx := t.Context()
	s := openStore(t, sqlitestore.Options{Readers: 1})
	if err := exec(ctx, s, "INSERT INTO records VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	held, err := s.Reader.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	destination := filepath.Join(t.TempDir(), "snapshot #?.db")
	if err := s.Exclusive(ctx, func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE; INSERT INTO records VALUES (2)"); err != nil {
			return err
		}
		return s.VacuumInto(ctx, destination)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.VacuumInto(ctx, destination); err == nil {
		t.Fatal("replaced a nonempty snapshot")
	}
	if v := pragma(t, ctx, held, "query_only"); v != 1 {
		t.Fatalf("reader query_only = %d", v)
	}
	copy, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: destination, RawQuery: "mode=ro"}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	count(t, copy, 1)
}
