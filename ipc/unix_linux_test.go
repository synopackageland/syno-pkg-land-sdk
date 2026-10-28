package ipc

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPackageSocketPaths(t *testing.T) {
	old := packageID
	t.Cleanup(func() { packageID = old })
	packageID = "SampleApp"
	for _, path := range []string{
		"/tmp/control.sock", "control.sock", "127.0.0.1:9000",
		"/var/packages/OtherApp/var/run/control.sock",
		"/var/packages/SampleApp/var/run/../control.sock",
		"/var/packages/SampleApp/var/run/sub/control.sock",
	} {
		if err := ServeUnix(context.Background(), path, http.NotFoundHandler()); err == nil {
			t.Errorf("accepted %q", path)
		}
		if _, err := DialUnix(context.Background(), path); err == nil {
			t.Errorf("dial accepted %q", path)
		}
	}
	if _, _, err := socketPath("/var/packages/SampleApp/var/run/control.sock"); err != nil {
		t.Fatal(err)
	}
	packageID = ""
	if _, _, err := socketPath("/var/packages/SampleApp/var/run/control.sock"); err == nil {
		t.Fatal("unbound identity accepted")
	}
}

func TestPrivateHTTPRoundTripAndCleanup(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveUnix(ctx, run, "control.sock", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(w, r.Body)
		}))
	}()
	path := filepath.Join(run, "control.sock")
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := os.Stat(path)
		if err == nil && info.Mode().Perm() == 0600 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("server failed: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(time.Millisecond)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialUnix(ctx, run, "control.sock")
	}}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Post("http://ipc/control", "application/octet-stream", strings.NewReader("private payload"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != "private payload" || response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	if _, err := dialUnix(ctx, run, "control.sock"); err == nil {
		t.Fatal("non-0600 socket accepted")
	}
	if err := serveUnix(ctx, run, "control.sock", http.NotFoundHandler()); err == nil {
		t.Fatal("existing socket replaced")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("owned socket retained: %v", err)
	}
}

func TestPrivateRunRefusesModesAndSymlinks(t *testing.T) {
	for _, mode := range []os.FileMode{0755, 0770, 0700 | os.ModeSticky} {
		run := filepath.Join(t.TempDir(), "run")
		if err := os.Mkdir(run, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(run, mode); err != nil {
			t.Fatal(err)
		}
		if fd, err := privateRun(run); err == nil {
			t.Fatalf("accepted mode %o fd=%d", mode, fd)
		}
	}
	base := t.TempDir()
	os.Symlink(t.TempDir(), filepath.Join(base, "run"))
	if _, err := privateRun(filepath.Join(base, "run")); err == nil {
		t.Fatal("run symlink accepted")
	}
}
