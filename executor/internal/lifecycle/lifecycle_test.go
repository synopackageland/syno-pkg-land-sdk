package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequestPersistsOfflineWithoutScheduler(t *testing.T) {
	for _, action := range []string{"stop", "purge"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			// No executor ever ran: its socket directory does not exist.
			cfg := Config{Package: "TestApp", Data: root, Socket: filepath.Join(root, "run", "executor.sock")}
			if err := Run(context.Background(), cfg, action); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "executor", "control")
			b, err := os.ReadFile(path)
			if err != nil || string(b) != action+"\n" {
				t.Fatal(string(b), err)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0600 {
				t.Fatal(info.Mode())
			}
		})
	}
}
func TestRequestWaitsForSocketAndTimeoutDoesNotBlockUninstall(t *testing.T) {
	for _, action := range []string{"stop", "purge"} {
		for _, timeout := range []bool{false, true} {
			t.Run(action+"-"+map[bool]string{false: "confirmed", true: "timeout"}[timeout], func(t *testing.T) {
				root := t.TempDir()
				os.Mkdir(filepath.Join(root, "run"), 0750)
				socket := filepath.Join(root, "run", "executor.sock")
				os.WriteFile(socket, []byte{}, 0600)
				warnings := []string{}
				result := ""
				cfg := Config{Package: "TestApp", Data: root, Socket: socket, Report: func(state string) { result = state }, Log: func(s string) { warnings = append(warnings, s) }}
				limit := 500 * time.Millisecond
				if timeout {
					limit = 50 * time.Millisecond
				}
				ctx, cancel := context.WithTimeout(context.Background(), limit)
				defer cancel()
				done := make(chan struct{})
				if !timeout {
					go func() {
						defer close(done)
						for {
							if _, err := os.Stat(filepath.Join(root, "executor", "control")); err == nil {
								// Pause closes the socket; purge ends with its directory.
								if action == "purge" {
									os.RemoveAll(filepath.Dir(socket))
								} else {
									os.Remove(socket)
								}
								return
							}
							select {
							case <-ctx.Done():
								return
							case <-time.After(time.Millisecond):
							}
						}
					}()
				} else {
					close(done)
				}
				if err := Run(ctx, cfg, action); err != nil {
					t.Fatal("uninstall was blocked:", err)
				}
				want := "cleanup-complete"
				if timeout {
					want = "cleanup-pending"
				}
				if result != want {
					t.Fatalf("state=%s want=%s", result, want)
				}
				<-done
				if !timeout && len(warnings) != 0 {
					t.Fatal("confirmation timed out", warnings)
				}
				if timeout && (len(warnings) != 1 || !strings.Contains(warnings[0], "timeout")) {
					t.Fatal("missing warning", warnings)
				}
				if _, err := os.Stat(filepath.Join(root, "executor", "control")); err != nil {
					t.Fatal("pending purge lost", err)
				}
			})
		}
	}
}

// A stopped package already paused the executor and removed its socket, so
// preuninst's purge must wait for the root purge itself: the executor removes
// its socket directory only after consuming purge. Returning earlier lets DSM
// delete the mailbox before the paused monitor polls it.
func TestPurgeOfPausedExecutorWaitsForSocketDirectoryRemoval(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "run")
	os.Mkdir(dir, 0750)
	cfg := Config{Package: "TestApp", Data: root, Socket: filepath.Join(dir, "executor.sock")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	consumed := make(chan time.Time, 1)
	go func() {
		for ctx.Err() == nil {
			if b, _ := os.ReadFile(filepath.Join(root, "executor", "control")); string(b) == "purge\n" {
				time.Sleep(300 * time.Millisecond)
				consumed <- time.Now()
				os.RemoveAll(dir)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	if err := Run(ctx, cfg, "purge"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-consumed:
	default:
		t.Fatal("purge returned before the executor removed its socket directory")
	}
	if ctx.Err() != nil {
		t.Fatal("purge confirmation timed out")
	}
}

func TestStopOfPausedExecutorDoesNotWaitForSocketDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "run")
	os.Mkdir(dir, 0750)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Run(ctx, Config{Package: "TestApp", Data: root, Socket: filepath.Join(dir, "executor.sock")}, "stop"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("stop waited for the socket directory")
	}
}

func TestRequestRejectsInvalidActionAndSymlink(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Package: "TestApp", Data: root, Socket: filepath.Join(root, "socket")}
	if Run(context.Background(), cfg, "resume") == nil {
		t.Fatal("invalid action accepted")
	}
	os.Mkdir(filepath.Join(root, "executor"), 0700)
	outside := filepath.Join(root, "outside")
	os.WriteFile(outside, []byte("safe"), 0600)
	os.Symlink(outside, filepath.Join(root, "executor", "control"))
	if Run(context.Background(), cfg, "purge") == nil {
		t.Fatal("symlink accepted")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "safe" {
		t.Fatal("outside changed")
	}
}

func TestInaccessibleSocketDoesNotBlockStopOrUninstall(t *testing.T) {
	// ENOTDIR is deterministic even under root, unlike chmod-based EACCES.
	// Both are status inspection failures, not control-file write failures.
	for _, action := range []string{"stop", "purge"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			blocked := filepath.Join(root, "blocked")
			os.WriteFile(blocked, []byte{}, 0600)
			logged := false
			cfg := Config{Package: "TestApp", Data: root, Socket: filepath.Join(blocked, "executor.sock"), Log: func(string) { logged = true }}
			if err := Run(context.Background(), cfg, action); err != nil {
				t.Fatal("socket inspection blocked lifecycle", err)
			}
			if !logged {
				t.Fatal("missing inaccessible socket warning")
			}
			b, err := os.ReadFile(filepath.Join(root, "executor", "control"))
			if err != nil || string(b) != action+"\n" {
				t.Fatal(string(b), err)
			}
		})
	}
}

func TestUnreadableSocketDirectoryStillPersistsStopAndPurge(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("EACCES requires an unprivileged process; ENOTDIR regression covers root")
	}
	for _, action := range []string{"stop", "purge"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "socket-dir")
			os.Mkdir(dir, 0700)
			os.WriteFile(filepath.Join(dir, "executor.sock"), nil, 0600)
			os.Chmod(dir, 0)
			defer os.Chmod(dir, 0700)
			logged := false
			cfg := Config{Package: "TestApp", Data: root, Socket: filepath.Join(dir, "executor.sock"), Log: func(string) { logged = true }}
			if _, err := os.Lstat(cfg.Socket); !os.IsPermission(err) {
				t.Fatal("fixture did not deny inspection", err)
			}
			if err := Run(context.Background(), cfg, action); err != nil {
				t.Fatal("permission denial blocked lifecycle", err)
			}
			if !logged {
				t.Fatal("missing warning")
			}
			b, err := os.ReadFile(filepath.Join(root, "executor", "control"))
			if err != nil || string(b) != action+"\n" {
				t.Fatal(string(b), err)
			}
		})
	}
}

func TestStartWritesResumeWithoutWaitingForSocket(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "socket")
	os.WriteFile(socket, []byte("still paused"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	if err := Run(ctx, Config{Package: "TestApp", Data: root, Socket: socket}, "start"); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("start waited for approval/socket")
	}
	b, err := os.ReadFile(filepath.Join(root, "executor/control"))
	if err != nil || string(b) != "start\n" {
		t.Fatal("missing resume request")
	}
}

// The admin's uninstall answer lands before the purge request the monitor acts on.
func TestPurgeWritesTheUninstallAnswerBeforeTheRequest(t *testing.T) {
	for deleteState, want := range map[bool]string{false: "keep-state\n", true: "delete-state\n"} {
		root := t.TempDir()
		cfg := Config{Package: "TestApp", Data: root, Socket: filepath.Join(root, "run", "executor.sock"), DeleteState: deleteState}
		if err := Run(context.Background(), cfg, "purge"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(root, "executor", "uninstall"))
		if err != nil || string(b) != want {
			t.Fatal(string(b), err)
		}
		if info, _ := os.Stat(filepath.Join(root, "executor", "uninstall")); info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
	}
	root := t.TempDir()
	if err := Run(context.Background(), Config{Package: "TestApp", Data: root, Socket: filepath.Join(root, "run", "executor.sock"), DeleteState: true}, "stop"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "executor", "uninstall")); !os.IsNotExist(err) {
		t.Fatal("stop wrote an uninstall answer", err)
	}
}

// A stop says whether it is for an upgrade, so the executor tells the App's
// stop step and jobs; a plain stop replaces an earlier upgrade answer.
func TestStopWritesItsReasonBeforeTheRequest(t *testing.T) {
	root := t.TempDir()
	for _, upgrade := range []bool{true, false} {
		cfg := Config{Package: "TestApp", Data: root, Socket: filepath.Join(root, "run", "executor.sock"), Upgrade: upgrade}
		if err := Run(context.Background(), cfg, "stop"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(root, "executor", "stop-reason"))
		if want := map[bool]string{true: "upgrade\n", false: "stop\n"}[upgrade]; err != nil || string(b) != want {
			t.Fatal(string(b), err)
		}
	}
}

func TestOfflinePurgeCannotProveCleanupFromAnAbsentRuntimeDirectory(t *testing.T) {
	root := t.TempDir()
	state := ""
	cfg := Config{Package: "TestApp", Data: root, Socket: filepath.Join(root, "run", "executor.sock"), Report: func(s string) { state = s }}
	if err := Run(context.Background(), cfg, "purge"); err != nil {
		t.Fatal(err)
	}
	if state != "cleanup-pending" {
		t.Fatalf("unobserved cleanup reported %s", state)
	}
}
