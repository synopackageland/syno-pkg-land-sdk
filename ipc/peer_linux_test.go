package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPeerServerRequiresExecutorBindingAndRoot(t *testing.T) {
	oldID, oldTier := packageID, executorTier
	t.Cleanup(func() { packageID, executorTier = oldID, oldTier })
	packageID = "SampleApp"
	h := func(context.Context, net.Conn, PeerCredentials) {}
	path := "/var/run/synopkgland/SampleApp/helper.sock"
	for _, tier := range []string{"", "none", "executor"} {
		executorTier = tier
		if os.Geteuid() != 0 || tier != "executor" {
			if _, err := StartPeerUnix(context.Background(), path, PeerPolicy{UID: 1000, GID: 1000}, h); err == nil {
				t.Fatalf("accepted server euid=%d tier=%q", os.Geteuid(), tier)
			}
			if err := ServePeerUnix(context.Background(), path, PeerPolicy{UID: 1000, GID: 1000}, h); err == nil {
				t.Fatalf("accepted blocking server euid=%d tier=%q", os.Geteuid(), tier)
			}
		}
	}
}

func TestPeerExecutorRoundTrip(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("run in an isolated root container to exercise the executor boundary")
	}
	oldID, oldTier := packageID, executorTier
	t.Cleanup(func() { packageID, executorTier = oldID, oldTier })
	packageID, executorTier = "PeerFixture", "executor"
	path := "/var/run/synopkgland/PeerFixture/helper.sock"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	admitted := make(chan PeerCredentials, 1)
	go func() {
		done <- ServePeerUnix(ctx, path, PeerPolicy{UID: 1000, GID: 1000},
			func(ctx context.Context, c net.Conn, p PeerCredentials) {
				admitted <- p
				var input string
				if err := json.NewDecoder(io.LimitReader(c, 64)).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				json.NewEncoder(c).Encode(input)
			})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := os.Stat(path)
		if err == nil && info.Mode().Perm() == 0660 {
			break
		}
		select {
		case err := <-done:
			t.Fatal(err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("peer server did not start")
		}
		time.Sleep(time.Millisecond)
	}
	defer os.RemoveAll(filepath.Dir(path))
	if err := ServePeerUnix(ctx, path, PeerPolicy{UID: 1000, GID: 1000}, func(context.Context, net.Conn, PeerCredentials) {}); err == nil {
		t.Fatal("existing peer socket replaced")
	}
	// Root can connect through filesystem permissions, but is not the selected UID.
	c, err := DialPeerUnix(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("unselected root peer admitted")
	}
	c.Close()
	select {
	case p := <-admitted:
		t.Fatalf("unexpected peer admitted: %+v", p)
	default:
	}
	exe, _ := os.Executable()
	// The Go test binary may live beneath a mode-0700 build directory.
	client := "/tmp/peer-fixture-client"
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(client, data, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(client)
	cmd := exec.Command(client, "-test.run=^TestPeerClientProcess$")
	cmd.Env = append(os.Environ(), "SDK_PEER_FIXTURE=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000}}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("client: %v\n%s", err, output)
	}
	if p := <-admitted; p.UID != 1000 || p.GID != 1000 {
		t.Fatalf("wrong identity: %+v", p)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := DialPeerUnix(ctx, path); err == nil {
		t.Fatal("wrong socket mode accepted")
	}
	os.Chmod(path, 0660)
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("peer socket retained: %v", err)
	}
}

func TestPeerClientProcess(t *testing.T) {
	if os.Getenv("SDK_PEER_FIXTURE") != "1" {
		t.Skip("subprocess fixture")
	}
	packageID = "PeerFixture"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := DialPeerUnix(ctx, "/var/run/synopkgland/PeerFixture/helper.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	json.NewEncoder(c).Encode("hello")
	var output string
	if err := json.NewDecoder(c).Decode(&output); err != nil || output != "hello" {
		t.Fatalf("round trip: %q %v", output, err)
	}
}

func TestPeerCredentialsOnRealUnixConnection(t *testing.T) {
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: t.TempDir() + "/peer.sock", Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan error, 1)
	go func() {
		c, err := l.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		p, err := peerCredentials(c)
		if err == nil && (p.UID != uint32(os.Geteuid()) || p.GID != uint32(os.Getegid()) || p.PID != int32(os.Getpid())) {
			t.Errorf("unexpected peer: %+v", p)
		}
		done <- err
	}()
	c, err := net.DialUnix("unix", nil, l.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPeerExecutorReplacesStaleSocketOnly(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("run in an isolated root container to exercise the executor boundary")
	}
	oldID, oldTier := packageID, executorTier
	t.Cleanup(func() { packageID, executorTier = oldID, oldTier })
	packageID, executorTier = fmt.Sprintf("PeerStale%d", os.Getpid()), "executor"
	fd, err := peerRun(true)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	run := "/var/run/synopkgland/" + packageID
	t.Cleanup(func() { os.RemoveAll(run) })
	path := filepath.Join(run, "helper.sock")
	policy := PeerPolicy{UID: 0, GID: 65534}
	handler := func(_ context.Context, c net.Conn, _ PeerCredentials) {
		defer c.Close()
		_, _ = io.WriteString(c, "live")
	}
	for _, kind := range []string{"stale", "file", "directory", "symlink", "foreign-owner", "wrong-mode"} {
		t.Run(kind, func(t *testing.T) {
			defer os.RemoveAll(path)
			switch kind {
			case "file":
				err = os.WriteFile(path, []byte("keep"), 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				err = os.Symlink("missing.sock", path)
			default:
				var l *net.UnixListener
				l, err = net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if err == nil {
					l.SetUnlinkOnClose(false)
					err = l.Close()
				}
				if err == nil {
					err = os.Chmod(path, 0660)
				}
				if err == nil && kind == "foreign-owner" {
					err = os.Chown(path, 65534, 65534)
				}
				if err == nil && kind == "wrong-mode" {
					err = os.Chmod(path, 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			s, err := StartPeerUnix(context.Background(), path, policy, handler)
			if kind != "stale" {
				if err == nil {
					s.Close()
					t.Fatal("unsafe existing path replaced")
				}
				after, err := os.Lstat(path)
				if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
					t.Fatalf("refused path changed: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("stale socket not replaced: %v", err)
			}
			defer s.Close()
			created, err := peerSocket(fd, "helper.sock")
			if err != nil || created.Gid != policy.GID {
				t.Fatalf("replacement ownership/mode: %+v %v", created, err)
			}
			if duplicate, err := StartPeerUnix(context.Background(), path, policy, handler); err == nil {
				duplicate.Close()
				t.Fatal("live socket replaced")
			}
			current, err := peerSocket(fd, "helper.sock")
			if err != nil || current.Ino != created.Ino || current.Dev != created.Dev {
				t.Fatalf("live socket changed: %v", err)
			}
			// Refusing a second server must leave the original server usable.
			c, err := DialPeerUnix(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(time.Second))
			data, err := io.ReadAll(c)
			if err != nil || string(data) != "live" {
				t.Fatalf("replacement round trip: %q %v", data, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("replacement socket retained: %v", err)
			}
		})
	}
}
