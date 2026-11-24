package sqlitefile

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOptOutAndMissingDatabaseAreNoOps(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data", "db")
	t.Setenv("SYNO_SQLITE_NOCOW", "0")
	if err := PrepareDir(dir); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("database directory = %v, %v", info, err)
	}
	path := filepath.Join(dir, "main.sqlite")
	unexpectedOpen := func(string) (*sql.DB, error) {
		t.Fatal("unexpected SQLite open")
		return nil, nil
	}
	if changed, err := ConvertToNoCOW(context.Background(), path, unexpectedOpen); err != nil || changed {
		t.Fatalf("opted-out conversion = %v, %v", changed, err)
	}
	t.Setenv("SYNO_SQLITE_NOCOW", "")
	if changed, err := ConvertToNoCOW(context.Background(), path, unexpectedOpen); err != nil || changed {
		t.Fatalf("missing database conversion = %v, %v", changed, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database path should still be absent: %v", err)
	}
	if err := PrepareDir(path); err != nil {
		t.Fatal(err)
	}
	if err := PrepareDir(filepath.Join(path, "blocked", "child")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareDir(filepath.Join(path, "file")); err == nil {
		t.Fatal("expected directory creation error for a file path")
	}
}
