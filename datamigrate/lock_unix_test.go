//go:build unix

package datamigrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const helperEnv = "DATAMIGRATE_TEST_HELPER_DIR"

// overlapMigrations are not idempotent on purpose: each step claims a marker
// file with O_EXCL while it runs and appends its version to applied.log, so
// two runs inside one step, or one step applied twice, show up.
func overlapMigrations(dir string) []Migration {
	var migrations []Migration
	for version := 1; version <= 3; version++ {
		migrations = append(migrations, Migration{Version: version, Name: fmt.Sprintf("step%d", version), Apply: func(Context) error {
			marker := filepath.Join(dir, "in-progress")
			f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return fmt.Errorf("another run is inside a step: %w", err)
			}
			f.Close()
			// Hold the step open long enough for other processes to reach it.
			time.Sleep(20 * time.Millisecond)
			log, err := os.OpenFile(filepath.Join(dir, "applied.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(log, "%d %d\n", version, os.Getpid())
			log.Close()
			if err != nil {
				return err
			}
			return os.Remove(marker)
		}})
	}
	return migrations
}

// TestHelperProcess is one of the concurrent processes in
// TestConcurrentProcessesApplyEachMigrationOnce: it waits until the parent
// closes its stdin, so all of them start together, then runs Migrate.
func TestHelperProcess(t *testing.T) {
	dir := os.Getenv(helperEnv)
	if dir == "" {
		t.Skip("helper process only")
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if _, err := Run(dir, Info{NewPkgVer: "2.0.0-0001"}, overlapMigrations(dir)); err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(3)
	}
}

func TestConcurrentProcessesApplyEachMigrationOnce(t *testing.T) {
	dir := t.TempDir()
	const processes = 8
	var stdins []io.WriteCloser
	var cmds []*exec.Cmd
	var outputs []*strings.Builder
	for range processes {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$", "-test.count=1")
		cmd.Env = append(os.Environ(), helperEnv+"="+dir)
		out := &strings.Builder{}
		cmd.Stdout, cmd.Stderr = out, out
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds, stdins, outputs = append(cmds, cmd), append(stdins, stdin), append(outputs, out)
	}
	for _, stdin := range stdins {
		stdin.Close()
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Errorf("process %d: %v\n%s", i, err, outputs[i])
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "applied.log"))
	if err != nil {
		t.Fatal(err)
	}
	var versions []int
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		version, err := strconv.Atoi(strings.Fields(line)[0])
		if err != nil {
			t.Fatalf("applied.log line %q", line)
		}
		versions = append(versions, version)
	}
	if fmt.Sprint(versions) != "[1 2 3]" {
		t.Fatalf("applied = %v, want each migration once in order\n%s", versions, raw)
	}
	if state := readState(t, dir); state.Schema != 3 || state.PackageVersion != "2.0.0-0001" {
		t.Fatalf("state = %+v", state)
	}
}

func TestConcurrentGoroutinesApplyEachMigrationOnce(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Run(dir, Info{}, overlapMigrations(dir))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "applied.log"))
	if n := strings.Count(string(raw), "\n"); n != 3 {
		t.Fatalf("applied %d steps, want 3:\n%s", n, raw)
	}
}

// holdLock takes LockFile as another process would and returns its release.
func holdLock(t *testing.T, dir string) func() {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, LockFile), os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return func() { f.Close() }
}

func TestUpToDateRunTakesNoLock(t *testing.T) {
	dir := t.TempDir()
	migration := Migration{Version: 1, Name: "one", Apply: func(Context) error { return nil }}
	if _, err := Run(dir, Info{NewPkgVer: "1.0.0-0001"}, []Migration{migration}); err != nil {
		t.Fatal(err)
	}
	release := holdLock(t, dir)
	defer release()
	// A cancelled context fails any run that would have to wait.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := RunContext(ctx, dir, Info{NewPkgVer: "1.0.0-0001"}, []Migration{migration})
	if err != nil || got.Schema != 1 {
		t.Fatalf("up-to-date run = %+v, %v; want schema 1 without waiting", got, err)
	}
	// A new package version is work: it waits for the lock.
	if _, err := RunContext(ctx, dir, Info{NewPkgVer: "1.0.1-0002"}, []Migration{migration}); !errors.Is(err, context.Canceled) {
		t.Fatalf("run with a new package version = %v, want context.Canceled", err)
	}
}

func TestCancelEndsTheWaitAndTheNextRunApplies(t *testing.T) {
	dir := t.TempDir()
	applied := 0
	migration := Migration{Version: 1, Name: "one", Apply: func(Context) error { applied++; return nil }}
	release := holdLock(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := RunContext(ctx, dir, Info{}, []Migration{migration})
		result <- err
	}()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait = %v, want context.Canceled", err)
	}
	if applied != 0 || readState(t, dir).Schema != 0 {
		t.Fatalf("a cancelled wait applied %d migrations", applied)
	}
	// A run queued behind the holder applies once the holder lets go.
	done := make(chan error, 1)
	go func() {
		_, err := Run(dir, Info{}, []Migration{migration})
		done <- err
	}()
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if applied != 1 || readState(t, dir).Schema != 1 {
		t.Fatalf("applied = %d, state = %+v", applied, readState(t, dir))
	}
}

func TestCancelStopsBeforeTheNextMigration(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	migrations := []Migration{
		{Version: 1, Name: "first", Apply: func(Context) error { cancel(); return nil }},
		{Version: 2, Name: "second", Apply: func(Context) error { t.Fatal("ran after cancel"); return nil }},
	}
	got, err := RunContext(ctx, dir, Info{}, migrations)
	if !errors.Is(err, context.Canceled) || got.Schema != 1 || readState(t, dir).Schema != 1 {
		t.Fatalf("RunContext = %+v, %v; want schema 1 recorded and context.Canceled", got, err)
	}
	if got, err := Run(dir, Info{}, migrations[:1]); err != nil || got.Schema != 1 {
		t.Fatalf("next run = %+v, %v", got, err)
	}
}

func TestLockFileIsAnEmptyFileInTheDataDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, Info{}, []Migration{{Version: 1, Name: "one", Apply: func(Context) error { return nil }}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(dir, LockFile))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Size() != 0 {
		t.Fatalf("lock file mode %v size %d", info.Mode(), info.Size())
	}
}
