//go:build linux

package supervision

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGroupCleanupOnLeaderExit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "child")
	cmd := exec.Command("sh", "-c", `sleep 60 & echo $! > "$1"; exit 7`, "fixture", path)
	p, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err == nil {
		t.Fatal("lost exit code")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		end := strings.LastIndex(string(raw), ")")
		if os.IsNotExist(err) || (end >= 0 && strings.HasPrefix(string(raw)[end+1:], " Z")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("group descendant survived leader exit")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestHostSlotsCancelWithoutLeaking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := AcquireStart(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a()
	b, err := AcquireStart(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b()
	done := make(chan error, 1)
	go func() {
		r, err := AcquireStart(ctx)
		if r != nil {
			r()
		}
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("third host slot admitted")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("slot wait not canceled")
	}
}
func TestNetworkWatcherCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- WatchNetwork(ctx, func() {}) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event watcher leaked descriptors or goroutines")
	}
}
