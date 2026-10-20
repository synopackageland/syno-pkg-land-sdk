package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/lifecycle"
)

func TestUpgradeGapPreservesRuntimeAndResumesOnlyApprovedPins(t *testing.T) {
	for _, gap := range []string{"directory", "INFO"} {
		for _, mode := range []string{"same pins", "new policy", "upgrade stop"} {
			t.Run(gap+"/"+mode, func(t *testing.T) {
				root := realTestPath(t, t.TempDir())
				pkg := filepath.Join(root, "package")
				target := filepath.Join(pkg, "target", "executor")
				data := filepath.Join(root, "data")
				state := filepath.Join(root, "protected")
				sockDir := filepath.Join(root, "socket")
				socket := filepath.Join(sockDir, "s")
				for _, dir := range []string{target, filepath.Join(data, "executor"), filepath.Join(state, "runtime"), filepath.Join(state, "delegations"), sockDir} {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				write := func(path, text string) {
					t.Helper()
					if err := os.WriteFile(path, []byte(text), 0600); err != nil {
						t.Fatal(err)
					}
				}
				info := filepath.Join(pkg, "INFO")
				write(info, "package=\"TestApp\"\n")
				write(filepath.Join(target, "executor"), "approved executable")
				write(filepath.Join(target, "policy.json"), "approved policy")
				markers := []string{filepath.Join(state, "runtime", "tasks.json"), filepath.Join(state, "delegations", "grant")}
				for _, marker := range markers {
					write(marker, "retained")
				}
				ed, err := digestFile(context.Background(), filepath.Join(target, "executor"), maxExecutorBytes)
				if err != nil {
					t.Fatal(err)
				}
				pd, err := digestFile(context.Background(), filepath.Join(target, "policy.json"), maxPolicyBytes)
				if err != nil {
					t.Fatal(err)
				}
				control := filepath.Join(data, "executor", "control")
				write(control, "start\n")
				m := newLifecycleMonitor("TestApp", pkg, control, func(string) {})
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				actions := make(chan string)
				var opened, closed, purged atomic.Int32
				approval := make(chan struct{}, 1)
				done := make(chan error, 1)
				finished := make(chan struct{})
				go func() {
					defer close(finished)
					done <- manageService(ctx, actions, func() (func(), error) {
						listener, err := net.Listen("unix", socket)
						if err != nil {
							return nil, err
						}
						opened.Add(1)
						return func() { listener.Close(); closed.Add(1) }, nil
					}, func(ctx context.Context) bool {
						// The production decision: only differing pins ask for approval.
						pins := packagePins(ctx, target, info, func(os.FileInfo) bool { return true }, ed, pd)
						return approvedPins(pins, func() error { return errors.New("no anchored upgrade here") }, func() error { return nil }, func(message string) {
							if strings.Contains(message, "administrator approval") {
								select {
								case approval <- struct{}{}:
								default:
								}
							}
						})
					}, func() error {
						purged.Add(1)
						return finishControl(state, filepath.Join(state, "runtime"), sockDir, true)
					})
				}()
				t.Cleanup(func() {
					cancel()
					select {
					case <-finished:
					case <-time.After(time.Second):
						t.Error("service did not exit")
					}
				})
				await := func(value *atomic.Int32, want int32) {
					t.Helper()
					for value.Load() != want {
						select {
						case <-ctx.Done():
							t.Fatal("service timeout")
						case <-time.After(time.Millisecond):
						}
					}
				}
				send := func(action string) {
					t.Helper()
					select {
					case actions <- action:
					case <-ctx.Done():
						t.Fatal("action timeout")
					}
				}
				start := time.Unix(1000, 0)
				if action := observeLifecycle(t, m, start); action != "start" {
					t.Fatal(action)
				} else {
					send(action)
				}
				await(&opened, 1)
				if mode == "upgrade stop" {
					write(control, "stop\n")
					if action := observeLifecycle(t, m, start); action != "stop" {
						t.Fatal(action)
					} else {
						send(action)
					}
					await(&closed, 1)
				}
				if gap == "directory" {
					if err := os.Rename(pkg, pkg+".upgrade"); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Remove(info); err != nil {
					t.Fatal(err)
				}
				if action := observeLifecycle(t, m, start); action != "stop" {
					t.Fatal("gap did not pause", action)
				} else {
					send(action)
				}
				await(&closed, 1)
				if conn, err := net.Dial("unix", socket); err == nil {
					conn.Close()
					t.Fatal("gap accepted a connection")
				}
				if action := observeLifecycle(t, m, start.Add(9*time.Minute)); action != "stop" {
					t.Fatal("gap resumed or purged", action)
				}
				if gap == "directory" {
					if err := os.Rename(pkg+".upgrade", pkg); err != nil {
						t.Fatal(err)
					}
				} else {
					write(info, "package=\"TestApp\"\n")
				}
				if mode != "same pins" {
					write(filepath.Join(target, "policy.json"), "new unapproved policy")
				}
				want := "start"
				if mode == "upgrade stop" {
					want = "stop"
				}
				if action := observeLifecycle(t, m, start.Add(9*time.Minute)); action != want {
					t.Fatalf("reappeared: got %q, want %q", action, want)
				} else {
					send(action)
				}
				if mode == "same pins" {
					await(&opened, 2)
					conn, err := net.Dial("unix", socket)
					if err != nil {
						t.Fatal("approved socket did not recover", err)
					}
					conn.Close()
				} else {
					if mode == "new policy" {
						select {
						case <-approval:
						case <-ctx.Done():
							t.Fatal("no approval-required result")
						}
					}
					if _, err := os.Stat(socket); !os.IsNotExist(err) {
						t.Fatal("unapproved socket exposed", err)
					}
				}
				if purged.Load() != 0 {
					t.Fatal("upgrade purged state")
				}
				for _, marker := range markers {
					if _, err := os.Stat(marker); err != nil {
						t.Fatal("upgrade lost retained state", err)
					}
				}
				// The real package-account helper writes the preuninst purge mailbox.
				hookDone := make(chan error, 1)
				go func() {
					hookDone <- lifecycle.Run(ctx, lifecycle.Config{Package: "TestApp", Data: data, Socket: socket}, "purge")
				}()
				for {
					b, err := os.ReadFile(control)
					if err == nil && string(b) == "purge\n" {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("preuninst mailbox timeout")
					case <-time.After(time.Millisecond):
					}
				}
				if action := observeLifecycle(t, m, start.Add(9*time.Minute)); action != "purge" {
					t.Fatal("preuninst purge delayed", action)
				} else {
					send(action)
				}
				select {
				case err := <-hookDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("preuninst timeout")
				}
				await(&purged, 1)
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("purge timeout")
				}
				for _, path := range []string{state, sockDir} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatal("purge remnant", path, err)
					}
				}
			})
		}
	}
}
