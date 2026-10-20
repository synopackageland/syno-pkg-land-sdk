package sharedfolder

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncAppliesOnFirstUseAndAfterEveryPathChange(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o755)
	var applied []string
	apply := func(path string) error { applied = append(applied, path); return nil }
	first := filepath.Join(cfg.Root, "volume1", "Media")

	if changed, err := cfg.Sync("media", apply); err != nil || !changed || len(applied) != 1 || applied[0] != first {
		t.Fatalf("first: %v %v %v", changed, err, applied)
	}
	if changed, err := cfg.Sync("media", apply); err != nil || changed || len(applied) != 1 {
		t.Fatalf("unchanged: %v %v %v", changed, err, applied)
	}
	// The share moves to another volume.
	second := filepath.Join(cfg.Root, "volume2", "Media")
	if err := os.MkdirAll(filepath.Dir(second), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(first, second); err != nil {
		t.Fatal(err)
	}
	if changed, err := cfg.Sync("media", apply); err != nil || !changed || len(applied) != 2 || applied[1] != second {
		t.Fatalf("moved: %v %v %v", changed, err, applied)
	}
}

func TestSyncFollowsAnotherChosenFolderOnTheSameKey(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o755)
	var applied []string
	apply := func(path string) error { applied = append(applied, path); return nil }
	if _, err := cfg.Sync("media", apply); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(cfg.Root, "volume1", "Other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, cfg.DataDir, `{"schema":1,"shared_folders":{"media":"Other"}}`)
	if changed, err := cfg.Sync("media", apply); err != nil || !changed || applied[len(applied)-1] != other {
		t.Fatalf("%v %v %v", changed, err, applied)
	}
}

func TestSyncKeepsTheOldRecordWhenApplyFails(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o755)
	boom := errors.New("storage refused")
	if _, err := cfg.Sync("media", func(string) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	var applied int
	if changed, err := cfg.Sync("media", func(string) error { applied++; return nil }); err != nil || !changed || applied != 1 {
		t.Fatalf("retry: %v %v %d", changed, err, applied)
	}
}

func TestSyncDoesNotApplyWithoutThePermission(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o555) // Write is required
	called := false
	_, err := cfg.Sync("media", func(string) error { called = true; return nil })
	if !errors.Is(err, ErrPermissionPending) || called {
		t.Fatalf("%v %v", err, called)
	}
}

func TestSyncIgnoresACorruptRecord(t *testing.T) {
	skipRoot(t)
	cfg := fixture(t, 0o755)
	if err := os.WriteFile(filepath.Join(cfg.DataDir, appliedFile), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := cfg.Sync("media", func(string) error { return nil }); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
}
