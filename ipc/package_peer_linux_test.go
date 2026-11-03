package ipc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func packagePeerHandler(_ context.Context, c net.Conn, _ PeerCredentials) {
	defer c.Close()
	_, _ = c.Write([]byte("ok"))
}
func allowPackagePeer(context.Context, PeerCredentials) bool { return true }
func packagePeerPath(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("package-account cases run in the nonroot subprocess")
	}
	return filepath.Join(t.TempDir(), "peers", "control.sock")
}

// Run real package-account tests and different-UID clients in a disposable root
// container; do not change the identity of the multithreaded test process.
func TestPackagePeerProcess(t *testing.T) {
	if os.Getenv("SDK_PACKAGE_PEER_CLIENT") != "" {
		path := os.Getenv("SDK_PACKAGE_PEER_PATH")
		c, err := net.DialTimeout("unix", path, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		b, err := io.ReadAll(c)
		if os.Getenv("SDK_PACKAGE_PEER_CLIENT") == "allow" {
			if err != nil || string(b) != "ok" {
				t.Fatalf("admission: %q %v", b, err)
			}
		} else if len(b) != 0 {
			t.Fatal("denied peer reached handler")
		}
		return
	}
	if os.Getenv("SDK_PACKAGE_PEER_SERVER") != "" {
		s, err := StartPackagePeerUnix(context.Background(), os.Getenv("SDK_PACKAGE_PEER_PATH"), func(_ context.Context, p PeerCredentials) bool { return p.UID == 1001 || p.UID == 1002 }, packagePeerHandler)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		_ = os.WriteFile(os.Getenv("SDK_PACKAGE_PEER_PATH")+".ready", nil, 0644)
		for i := 0; i < 1000; i++ {
			if _, err := os.Stat(os.Getenv("SDK_PACKAGE_PEER_PATH") + ".done"); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("fixture timed out")
	}
	if os.Geteuid() != 0 {
		return
	}
	if _, err := StartPackagePeerUnix(context.Background(), "/tmp/peers/control.sock", allowPackagePeer, packagePeerHandler); err == nil {
		t.Fatal("root admitted")
	}
	dir, err := os.MkdirTemp("/tmp", "package-peer-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "test")
	if err := os.WriteFile(binary, data, 0755); err != nil {
		t.Fatal(err)
	}
	command := func(uid uint32, env ...string) *exec.Cmd {
		cmd := exec.Command(binary, "-test.run=^TestPackagePeer", "-test.timeout=30s")
		cmd.Env = append(os.Environ(), append([]string{"SDK_PACKAGE_PEER_FOREIGN=" + filepath.Join(dir, "foreign")}, env...)...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid}}
		return cmd
	}
	foreign := filepath.Join(dir, "foreign")
	if err := os.Mkdir(foreign, 0711); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(foreign, 0711); err != nil {
		t.Fatal(err)
	}
	if out, err := command(1000).CombinedOutput(); err != nil {
		t.Fatalf("package account: %v\n%s", err, out)
	}
	public := filepath.Join(dir, "public")
	if err := os.Mkdir(public, 0711); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(public, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(public, "control.sock")
	server := command(1000, "SDK_PACKAGE_PEER_SERVER=1", "SDK_PACKAGE_PEER_PATH="+path)
	server.Args = []string{binary, "-test.run=^TestPackagePeerProcess$", "-test.timeout=30s"}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
	for i := 0; ; i++ {
		if _, err := os.Stat(path + ".ready"); err == nil {
			break
		}
		if i > 500 {
			t.Fatal("server not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, uid := range []uint32{1001, 1002, 1003, 0} {
		decision := "deny"
		if uid == 1001 || uid == 1002 {
			decision = "allow"
		}
		cmd := command(uid, "SDK_PACKAGE_PEER_CLIENT="+decision, "SDK_PACKAGE_PEER_PATH="+path)
		cmd.Args = []string{binary, "-test.run=^TestPackagePeerProcess$", "-test.timeout=10s"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("client uid=%d: %v\n%s", uid, err, out)
		}
	}
	if err := os.WriteFile(path+".done", nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := server.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestPackagePeerAuthorizationAndLifecycle(t *testing.T) {
	for _, decision := range []string{"nil", "deny", "allow"} {
		t.Run(decision, func(t *testing.T) {
			path := packagePeerPath(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var called atomic.Bool
			var authorize PeerAuthorizer
			if decision != "nil" {
				authorize = func(_ context.Context, p PeerCredentials) bool {
					if p.UID != uint32(os.Geteuid()) || p.GID != uint32(os.Getegid()) || p.PID != int32(os.Getpid()) {
						t.Errorf("credentials: %+v", p)
					}
					return decision == "allow"
				}
			}
			s, err := StartPackagePeerUnix(ctx, path, authorize, func(_ context.Context, c net.Conn, _ PeerCredentials) {
				called.Store(true)
				_, _ = c.Write([]byte("ok"))
			})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if st, err := os.Stat(filepath.Dir(path)); err != nil || st.Mode().Perm() != 0711 {
				t.Fatalf("directory: %v %v", st, err)
			}
			if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0666 {
				t.Fatalf("socket: %v %v", st, err)
			}
			if _, err := StartPackagePeerUnix(ctx, path, authorize, packagePeerHandler); err == nil {
				t.Fatal("live socket replaced")
			}
			c, err := net.DialTimeout("unix", path, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(time.Second))
			b := make([]byte, 2)
			_, err = io.ReadFull(c, b)
			if decision == "allow" {
				if err != nil || string(b) != "ok" {
					t.Fatalf("authorized: %q %v", b, err)
				}
				if err := s.StopAccepting(); err != nil {
					t.Fatal(err)
				}
				_ = c.SetReadDeadline(time.Time{})
			} else if err == nil || called.Load() {
				t.Fatal("default/explicit denial bypassed")
			}
			cancel()
			_ = s.Close()
			if decision != "allow" && !errors.Is(s.Wait(), context.Canceled) {
				t.Fatalf("cancellation: %v", s.Wait())
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("socket retained: %v", err)
			}
		})
	}
}

func TestPackagePeerUnsafePathsAndStaleCleanup(t *testing.T) {
	for _, kind := range []string{"directory-mode", "directory-symlink", "file", "socket-mode", "socket-symlink", "stale", "live", "replacement"} {
		t.Run(kind, func(t *testing.T) {
			path := packagePeerPath(t)
			dir := filepath.Dir(path)
			if err := os.Mkdir(dir, 0711); err != nil {
				t.Fatal(err)
			}
			var l *net.UnixListener
			switch kind {
			case "directory-mode":
				os.Chmod(dir, 0777)
			case "directory-symlink":
				os.Remove(dir)
				os.Symlink(t.TempDir(), dir)
			case "file":
				os.WriteFile(path, []byte("keep"), 0666)
			case "socket-symlink":
				os.Symlink("missing.sock", path)
			default:
				var err error
				l, err = net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				l.SetUnlinkOnClose(false)
				defer l.Close()
				os.Chmod(path, 0666)
				if kind != "live" {
					l.Close()
				}
				if kind == "socket-mode" {
					os.Chmod(path, 0600)
				}
			}
			s, err := StartPackagePeerUnix(context.Background(), path, allowPackagePeer, packagePeerHandler)
			if kind == "stale" || kind == "replacement" {
				if err != nil {
					t.Fatal(err)
				}
				if kind == "replacement" {
					os.Remove(path)
					os.WriteFile(path, []byte("keep"), 0600)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				if kind == "replacement" {
					if b, err := os.ReadFile(path); err != nil || string(b) != "keep" {
						t.Fatal("replacement removed")
					}
				}
			} else {
				if err == nil {
					s.Close()
					t.Fatal("unsafe path accepted")
				}
				if _, err := os.Lstat(path); kind != "directory-mode" && kind != "directory-symlink" && err != nil {
					t.Fatal("existing path removed")
				}
			}
		})
	}
}

func TestPackagePeerBoundsAndCancellation(t *testing.T) {
	path := packagePeerPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 8)
	s, err := StartPackagePeerUnix(ctx, path, func(ctx context.Context, _ PeerCredentials) bool { entered <- struct{}{}; <-ctx.Done(); return false }, packagePeerHandler)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 8; i++ {
		c, err := net.DialTimeout("unix", path, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("authorization not started")
		}
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 1)
	if n, err := c.Read(b); n != 0 || err == nil {
		t.Fatal("ninth peer admitted")
	}
	cancel()
	if err := s.Close(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPackagePeerForeignOwnership(t *testing.T) {
	if os.Geteuid() == 0 {
		return
	}
	dir := os.Getenv("SDK_PACKAGE_PEER_FOREIGN")
	if dir == "" {
		t.Skip("requires foreign-owner subprocess fixture")
	}
	if s, err := StartPackagePeerUnix(context.Background(), filepath.Join(dir, "control.sock"), allowPackagePeer, packagePeerHandler); err == nil {
		s.Close()
		t.Fatal("foreign directory admitted")
	}
}

func TestPackagePeerConfiguredRetainedCapacity(t *testing.T) {
	path := packagePeerPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const limit = 316
	entered := make(chan struct{}, limit)
	s, err := StartPackagePeerUnixWithOptions(ctx, path, allowPackagePeer, func(ctx context.Context, c net.Conn, _ PeerCredentials) {
		defer c.Close()
		if err := c.SetDeadline(time.Time{}); err != nil {
			return
		}
		entered <- struct{}{}
		<-ctx.Done()
	}, PackagePeerOptions{MaxConnections: limit})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for range limit {
		c, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("configured retained capacity not available")
		}
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 1)
	if _, err := c.Read(b); err == nil {
		t.Fatal("overflow peer admitted")
	} else if n, ok := err.(net.Error); ok && n.Timeout() {
		t.Fatal("overflow peer queued without a bound")
	}
	cancel()
	if err := s.Close(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
