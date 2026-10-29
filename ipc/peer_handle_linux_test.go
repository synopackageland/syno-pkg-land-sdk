package ipc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Build a separate executable with the actual executor binding. It stops and
// reaps a child, stops admission, writes its ACK, returns from the handler and
// keeps the verified connection open until the executable exits.
func TestPeerHandleExecutorProcessExit(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated Linux root container")
	}
	oldID := packageID
	packageID = "PeerShutdownFixture"
	t.Cleanup(func() { packageID = oldID })
	path := "/var/run/synopkgland/PeerShutdownFixture/helper.sock"
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(path)) })
	exe := filepath.Join(t.TempDir(), "executor")
	build := exec.Command("go", "build", "-ldflags", "-X github.com/synopackageland/syno-pkg-land-sdk/ipc.packageID=PeerShutdownFixture -X github.com/synopackageland/syno-pkg-land-sdk/ipc.executorTier=executor", "-o", exe, "./testdata/peer-shutdown")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build executor: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	lines := bufio.NewScanner(stdout)
	if !lines.Scan() || lines.Text() != "ready" {
		t.Fatal("executor not ready; see subprocess stderr")
	}
	// Dial as the selected UID in a subprocess; root is deliberately rejected.
	client := filepath.Join("/tmp", "t0364-peer-client")
	data, err := os.ReadFile(mustExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(client, data, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(client)
	clientCmd := exec.CommandContext(ctx, client, "-test.run=^TestPeerHandleClientProcess$")
	clientCmd.Env = append(os.Environ(), "SDK_PEER_HANDLE_CLIENT=1")
	clientCmd.SysProcAttr = peerClientCredential()
	clientOut, err := clientCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	clientIn, err := clientCmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	clientCmd.Stderr = os.Stderr
	if err := clientCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { clientCmd.Process.Kill(); clientCmd.Wait() }()
	clientLines := bufio.NewScanner(clientOut)
	if !clientLines.Scan() || clientLines.Text() != "ack" {
		t.Fatalf("client ACK: %q; see subprocess stderr", clientLines.Text())
	}
	if !lines.Scan() || lines.Text() != "retained" {
		t.Fatal("handler did not retain peer; see subprocess stderr")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket retained: %v", err)
	}
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		c.Close()
		t.Fatal("new dial admitted after stop")
	}
	// The client verifies no EOF while the server process remains alive.
	io.WriteString(clientIn, "check\n")
	if !clientLines.Scan() || clientLines.Text() != "open" {
		t.Fatalf("connection closed early: %q; see subprocess stderr", clientLines.Text())
	}
	io.WriteString(stdin, "x")
	if err := cmd.Wait(); err != nil {
		t.Fatalf("executor exit: %v; see subprocess stderr", err)
	}
	io.WriteString(clientIn, "exit\n")
	if err := clientCmd.Wait(); err != nil {
		t.Fatalf("client EOF: %v; see subprocess stderr", err)
	}
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func peerClientCredential() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000}}
}

func TestPeerHandleClientProcess(t *testing.T) {
	if os.Getenv("SDK_PEER_HANDLE_CLIENT") != "1" {
		t.Skip("subprocess fixture")
	}
	packageID = "PeerShutdownFixture"
	c, err := DialPeerUnix(context.Background(), "/var/run/synopkgland/PeerShutdownFixture/helper.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(c, "stop\n"); err != nil {
		t.Fatal(err)
	}
	ack := make([]byte, len("{\"ok\":true,\"child_reaped\":true}\n"))
	if _, err := io.ReadFull(c, ack); err != nil {
		t.Fatal(err)
	}
	if string(ack) != "{\"ok\":true,\"child_reaped\":true}\n" {
		t.Fatalf("ACK: %q", ack)
	}
	fmt.Println("ack")
	control := bufio.NewScanner(os.Stdin)
	if !control.Scan() {
		t.Fatal("missing check command")
	}
	c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var b [1]byte
	if _, err := c.Read(b[:]); err == nil {
		t.Fatal("unexpected data")
	} else if n, ok := err.(net.Error); !ok || !n.Timeout() {
		t.Fatalf("EOF before process exit: %v", err)
	}
	fmt.Println("open")
	if !control.Scan() {
		t.Fatal("missing exit command")
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if data, err := io.ReadAll(c); err != nil || len(data) != 0 {
		t.Fatalf("real EOF: %q %v", data, err)
	}
}

func TestPeerHandleRetainedLimitAndClose(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated Linux root container")
	}
	oldID, oldTier := packageID, executorTier
	packageID, executorTier = "PeerHandleLimitFixture", "executor"
	t.Cleanup(func() { packageID, executorTier = oldID, oldTier })
	path := "/var/run/synopkgland/PeerHandleLimitFixture/helper.sock"
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(path)) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admitted := make(chan net.Conn, 9)
	server, err := StartPeerUnix(ctx, path, PeerPolicy{}, func(ctx context.Context, c net.Conn, _ PeerCredentials) { admitted <- c })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	dial := func() net.Conn {
		t.Helper()
		c, err := DialPeerUnix(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(3 * time.Second))
		t.Cleanup(func() { c.Close() })
		return c
	}
	peers := make([]net.Conn, 8)
	var held net.Conn
	for i := range peers {
		peers[i] = dial()
		select {
		case held = <-admitted:
		case <-time.After(3 * time.Second):
			t.Fatal("not admitted")
		}
	}
	// Callback return must not release a connection's slot.
	rejected := dial()
	if _, err := rejected.Read(make([]byte, 1)); err == nil {
		t.Fatal("ninth live peer admitted")
	}
	select {
	case <-admitted:
		t.Fatal("rejected peer reached handler")
	default:
	}
	held.Close()
	last := dial()
	select {
	case held = <-admitted:
	case <-time.After(3 * time.Second):
		t.Fatal("closed slot not reused")
	}
	if err := server.StopAccepting(); err != nil {
		t.Fatal(err)
	}
	if err := server.StopAccepting(); err != nil {
		t.Fatal(err)
	}
	if err := server.Wait(); err != nil {
		t.Fatal(err)
	}
	// Admission stop preserves context and the accepted peer.
	select {
	case <-ctx.Done():
		t.Fatal("StopAccepting canceled context")
	default:
	}
	if _, err := held.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := io.ReadFull(last, b[:]); err != nil || b[0] != 'x' {
		t.Fatalf("retained peer: %q %v", b, err)
	}
	// Cancellation must still close retained connections after StopAccepting.
	cancel()
	if _, err := last.Read(b[:]); err != io.EOF {
		t.Fatalf("cancel did not close retained peer: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPeerHandleCloseCancelsHandlerAndConnections(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated Linux root container")
	}
	oldID, oldTier := packageID, executorTier
	packageID, executorTier = "PeerHandleCloseFixture", "executor"
	t.Cleanup(func() { packageID, executorTier = oldID, oldTier })
	path := "/var/run/synopkgland/PeerHandleCloseFixture/helper.sock"
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(path)) })
	handlerContext := make(chan context.Context, 1)
	server, err := StartPeerUnix(context.Background(), path, PeerPolicy{}, func(ctx context.Context, c net.Conn, _ PeerCredentials) { handlerContext <- ctx })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	c, err := DialPeerUnix(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var handlerCtx context.Context
	select {
	case handlerCtx = <-handlerContext:
	case <-time.After(3 * time.Second):
		t.Fatal("not admitted")
	}
	if err := server.StopAccepting(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerCtx.Done():
		t.Fatal("admission stop canceled handler")
	default:
	}
	// Concurrent repeated lifecycle calls must converge without closing a channel
	// twice or waiting for a callback that itself could be calling StopAccepting.
	results := make(chan error, 16)
	for range 8 {
		go func() { results <- server.Close() }()
		go func() { results <- server.StopAccepting() }()
	}
	for range 16 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("lifecycle deadlock")
		}
	}
	select {
	case <-handlerCtx.Done():
	default:
		t.Fatal("Close did not cancel handler")
	}
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("Close did not close peer: %v", err)
	}
}

func TestPeerHandleClosedConnectionsKeepActiveCallbackLimit(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated Linux root container")
	}
	oldID, oldTier := packageID, executorTier
	packageID, executorTier = "PeerCallbackLimitFixture", "executor"
	t.Cleanup(func() { packageID, executorTier = oldID, oldTier })
	path := "/var/run/synopkgland/PeerCallbackLimitFixture/helper.sock"
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(path)) })
	admitted := make(chan struct{}, 9)
	server, err := StartPeerUnix(context.Background(), path, PeerPolicy{}, func(ctx context.Context, c net.Conn, _ PeerCredentials) {
		c.Close()
		admitted <- struct{}{}
		<-ctx.Done()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for i := 0; i < 9; i++ {
		c, err := DialPeerUnix(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, err = c.Read(make([]byte, 1))
		c.Close()
		if err != io.EOF {
			t.Fatalf("closed peer: %v", err)
		}
		if i < 8 {
			select {
			case <-admitted:
			case <-time.After(3 * time.Second):
				t.Fatal("not admitted")
			}
		}
	}
	if err := server.StopAccepting(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-admitted:
		t.Fatal("ninth concurrent callback admitted")
	default:
	}
}

func TestPeerHandleInitialDeadline(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated Linux root container")
	}
	oldID, oldTier := packageID, executorTier
	packageID, executorTier = "PeerDeadlineFixture", "executor"
	t.Cleanup(func() { packageID, executorTier = oldID, oldTier })
	path := "/var/run/synopkgland/PeerDeadlineFixture/helper.sock"
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(path)) })
	readErr := make(chan error, 1)
	server, err := StartPeerUnix(context.Background(), path, PeerPolicy{}, func(ctx context.Context, c net.Conn, _ PeerCredentials) {
		defer c.Close()
		_, err := c.Read(make([]byte, 1))
		readErr <- err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	c, err := DialPeerUnix(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case err := <-readErr:
		if n, ok := err.(net.Error); !ok || !n.Timeout() {
			t.Fatalf("initial deadline: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("initial peer deadline not enforced")
	}
}
