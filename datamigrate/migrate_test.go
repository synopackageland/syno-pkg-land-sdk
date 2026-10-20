package datamigrate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func readState(t *testing.T, dir string) State {
	t.Helper()
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return got
}

func TestMissingStateIsZero(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != (State{}) {
		t.Fatalf("missing state = %+v, want zero", got)
	}
}

func TestRunAppliesEveryMigrationInVersionOrderAndRecordsSchema(t *testing.T) {
	dir := t.TempDir()
	var order []int
	migrations := []Migration{
		{Version: 2, Name: "second", Apply: func(Context) error { order = append(order, 2); return nil }},
		{Version: 1, Name: "first", Apply: func(Context) error { order = append(order, 1); return nil }},
	}
	got, err := Run(dir, Info{NewPkgVer: "1.1.0-0002"}, migrations)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []int{1, 2}; !reflect.DeepEqual(order, want) {
		t.Fatalf("migration order = %v, want %v", order, want)
	}
	if got.Schema != 2 {
		t.Fatalf("returned schema = %d, want 2", got.Schema)
	}
	if reread := readState(t, dir); reread.Schema != 2 || reread.PackageVersion != "1.1.0-0002" {
		t.Fatalf("recorded state = %+v", reread)
	}
}

func TestRunSkipsMigrationsAlreadyRecorded(t *testing.T) {
	dir := t.TempDir()
	runs := 0
	migration := Migration{Version: 1, Name: "once", Apply: func(Context) error { runs++; return nil }}
	for range 2 {
		if _, err := Run(dir, Info{}, []Migration{migration}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	if runs != 1 {
		t.Fatalf("migration applied %d times, want 1", runs)
	}
}

func TestRunStopsAtFailureAndKeepsEarlierVersion(t *testing.T) {
	dir := t.TempDir()
	sentinel := errors.New("boom")
	applied := 0
	migrations := []Migration{
		{Version: 1, Name: "ok", Apply: func(Context) error { applied++; return nil }},
		{Version: 2, Name: "bad", Apply: func(Context) error { applied++; return sentinel }},
		{Version: 3, Name: "later", Apply: func(Context) error { applied++; return nil }},
	}
	_, err := Run(dir, Info{}, migrations)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run error = %v, want the migration error", err)
	}
	if applied != 2 {
		t.Fatalf("applied %d migrations, want 2", applied)
	}
	if got := readState(t, dir); got.Schema != 1 {
		t.Fatalf("recorded schema = %d, want 1 (last success)", got.Schema)
	}
}

func TestRunLeavesOldDataWhenAMigrationFails(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "old.db")
	if err := os.WriteFile(keep, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(dir, Info{}, []Migration{{Version: 1, Name: "bad", Apply: func(Context) error { return errors.New("nope") }}}); err == nil {
		t.Fatal("want error")
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "old" {
		t.Fatalf("old data = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, StateFile)); !os.IsNotExist(err) {
		t.Fatalf("a failed first migration must not record a version: %v", err)
	}
}

func TestRunGivesMigrationsTheOldAndNewPackageVersion(t *testing.T) {
	dir := t.TempDir()
	var first Context
	if _, err := Run(dir, Info{OldPkgVer: "1.0.0-0001", NewPkgVer: "1.1.0-0002"}, []Migration{
		{Version: 1, Name: "m1", Apply: func(ctx Context) error { first = ctx; return nil }},
	}); err != nil {
		t.Fatal(err)
	}
	if first.OldPkgVer != "1.0.0-0001" || first.NewPkgVer != "1.1.0-0002" || first.DataDir != dir {
		t.Fatalf("context = %+v", first)
	}
	var second Context
	if _, err := Run(dir, Info{NewPkgVer: "1.2.0-0003"}, []Migration{
		{Version: 2, Name: "m2", Apply: func(ctx Context) error { second = ctx; return nil }},
	}); err != nil {
		t.Fatal(err)
	}
	if second.OldPkgVer != "1.1.0-0002" {
		t.Fatalf("old package version without SYNOPKG_OLD_PKGVER = %q, want the recorded one", second.OldPkgVer)
	}
}

func TestRunRecordsThePackageVersionWithoutMigrations(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, Info{NewPkgVer: "1.0.0-0001"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := readState(t, dir); got.Schema != 0 || got.PackageVersion != "1.0.0-0001" {
		t.Fatalf("state = %+v", got)
	}
	if _, err := Run(dir, Info{NewPkgVer: "1.1.0-0002"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := readState(t, dir); got.PackageVersion != "1.1.0-0002" {
		t.Fatalf("state = %+v", got)
	}
}

func TestRunRejectsInvalidMigrationLists(t *testing.T) {
	dir := t.TempDir()
	ok := func(Context) error { return nil }
	for name, migrations := range map[string][]Migration{
		"zero version": {{Version: 0, Name: "z", Apply: ok}},
		"negative":     {{Version: -1, Name: "n", Apply: ok}},
		"duplicate":    {{Version: 1, Name: "a", Apply: ok}, {Version: 1, Name: "b", Apply: ok}},
		"nil apply":    {{Version: 1, Name: "a"}},
	} {
		if _, err := Run(dir, Info{}, migrations); err == nil {
			t.Errorf("%s: want an error before anything runs", name)
		}
	}
}

func TestRunRejectsAnEmptyDataDirectory(t *testing.T) {
	if _, err := Run("", Info{}, nil); err == nil {
		t.Fatal("want an error")
	}
}

func TestLoadRejectsCorruptState(t *testing.T) {
	for name, body := range map[string]string{
		"not json":     "{",
		"negative":     `{"schema":-1}`,
		"not a number": `{"schema":"one"}`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, StateFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
