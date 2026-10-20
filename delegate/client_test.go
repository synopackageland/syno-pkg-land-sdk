package delegate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func executor(t *testing.T, serve func(Request) string) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "ds-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, e := l.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				var q Request
				json.NewDecoder(bufio.NewReader(conn)).Decode(&q)
				_, _ = conn.Write([]byte(serve(q)))
			}()
		}
	}()
	return &Client{Socket: socket, Timeout: 100 * time.Millisecond}
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}
func TestClientProtocolAndStableRefusals(t *testing.T) {
	for _, stable := range []string{"DENIED", "DELEGATION_EXPIRED", "SCOPE_DENIED", "ACCOUNT_DISABLED", "POLICY_DENIED", "FILE_EXISTS", "FUTURE_CODE"} {
		t.Run(stable, func(t *testing.T) {
			c := executor(t, func(q Request) string {
				if q.V != 1 || q.Op != "status" {
					t.Errorf("wire: %+v", q)
				}
				return `{"ok":false,"code":"` + stable + `","message":"secret cookie"}` + "\n"
			})
			code(t, c.Call(context.Background(), "status", nil, nil, nil), stable)
		})
	}
}
func TestClientUnavailableTimeoutAndCancellation(t *testing.T) {
	c := &Client{Socket: filepath.Join(t.TempDir(), "absent")}
	code(t, c.Call(context.Background(), "status", nil, nil, nil), "EXECUTOR_UNAVAILABLE")
	// The executor holds the reply until the client has timed out, so a loaded
	// machine cannot close the connection first.
	timedOut := make(chan struct{})
	c = executor(t, func(Request) string { <-timedOut; return "" })
	code(t, c.Call(context.Background(), "status", nil, nil, nil), "EXECUTOR_TIMEOUT")
	close(timedOut)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code(t, c.Call(ctx, "status", nil, nil, nil), "canceled")
}
func TestClientBoundsAndMalformedResponses(t *testing.T) {
	for _, response := range []string{`{}`, `{"ok":true}`, `{"ok":true,"result":{}} trailing` + "\n", strings.Repeat("x", MaxFrame+1), `{"ok":true,"result":{}}`} {
		t.Run(response[:min(len(response), 24)], func(t *testing.T) {
			c := executor(t, func(Request) string { return response })
			if c.Call(context.Background(), "status", nil, nil, nil) == nil {
				t.Fatal("accepted invalid frame")
			}
		})
	}
	c := executor(t, func(Request) string { t.Error("oversized request sent"); return "" })
	code(t, c.Call(context.Background(), "status", nil, map[string]string{"x": strings.Repeat("x", MaxFrame)}, nil), "EXECUTOR_TOO_LARGE")
}
func TestFilesStagingAndTypedOperations(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	seen := map[string]bool{}
	c := executor(t, func(q Request) string {
		seen[q.Op] = true
		if q.Auth != nil {
			t.Error("offline op had auth")
		}
		var p map[string]any
		json.Unmarshal(q.Params, &p)
		if p["delegation_id"] != "d1" || p["job_id"] != "j1" {
			t.Errorf("binding %+v", p)
		}
		if q.Op == "files.write" {
			name := p["staging"].(string)
			if filepath.Base(name) != name || name == "chosen.txt" {
				t.Error("unsafe staging")
			}
			b, e := os.ReadFile(filepath.Join(dir, name))
			if e != nil || string(b) != "content" {
				t.Errorf("staged %q %v", b, e)
			}
		}
		switch q.Op {
		case "files.list":
			for key := range p {
				if key != "path" && key != "delegation_id" && key != "job_id" {
					return `{"ok":false,"code":"INVALID_REQUEST"}` + "\n"
				}
			}
			return `{"ok":true,"result":{"files":[],"total":0,"offset":0}}` + "\n"
		case "files.info":
			return `{"ok":true,"result":{"files":[{"path":"/home","name":"home","isdir":true}]}}` + "\n"
		default:
			return `{"ok":true,"result":{}}` + "\n"
		}
	})
	f := JobContext{Client: c, JobID: "j1", DelegationID: "d1", StagingDir: dir}.Files()
	if _, e := f.List(context.Background(), "/home", ListOptions{}); e != nil {
		t.Fatal(e)
	}
	if info, e := f.Info(context.Background(), "/home"); e != nil || !info.IsDir || info.Path != "/home" {
		t.Fatalf("info: %+v %v", info, e)
	}
	if e := f.CreateFolder(context.Background(), "/home", "new"); e != nil {
		t.Fatal(e)
	}
	if e := f.CheckPermission(context.Background(), "/home"); e != nil {
		t.Fatal(e)
	}
	if e := f.Write(context.Background(), "/home", "chosen.txt", strings.NewReader("content"), 7); e != nil {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 || len(seen) != 5 {
		t.Fatalf("cleanup %v ops %v", entries, seen)
	}
}
func TestStagingCleanupOnRefusalLimitAndReadFailure(t *testing.T) {
	c := executor(t, func(Request) string { return `{"ok":false,"code":"DENIED"}` + "\n" })
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	f := JobContext{Client: c, JobID: "j", DelegationID: "d", StagingDir: dir}.Files()
	code(t, f.Write(context.Background(), "/home", "a", strings.NewReader("abc"), 3), "DENIED")
	code(t, f.Write(context.Background(), "/home", "../bad", strings.NewReader("a"), 1), CodeInvalidRequest)
	code(t, f.Write(context.Background(), "/home", "a", strings.NewReader("abc"), 2), "limit-exceeded")
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatal("staging leaked")
	}
}

func TestClientRejectsNonObjectAndInvalidRefusalCodes(t *testing.T) {
	for _, response := range []string{`{"ok":true,"result":[]}`, `{"ok":false,"code":"secret\nvalue"}`} {
		c := executor(t, func(Request) string { return response + "\n" })
		code(t, c.Call(context.Background(), "status", nil, nil, nil), "EXECUTOR_PROTOCOL")
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("source failed") }
func TestWriteCleansStagingWhenSourceFails(t *testing.T) {
	c := executor(t, func(Request) string { t.Error("failed read reached executor"); return "" })
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	f := JobContext{Client: c, JobID: "j", DelegationID: "d", StagingDir: dir}.Files()
	code(t, f.Write(context.Background(), "/home", "file", brokenReader{}, 3), "STAGING_UNAVAILABLE")
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatal("staging leaked")
	}
}

func TestDialFailureIsDistinctFromAcceptedRequestFailure(t *testing.T) {
	c := &Client{Socket: filepath.Join(t.TempDir(), "missing")}
	var dial *dialFailure
	err := c.Call(context.Background(), "files.write", nil, nil, nil)
	if !errors.As(err, &dial) {
		t.Fatalf("missing socket must be safe to retry: %v", err)
	}
	code(t, err, "EXECUTOR_UNAVAILABLE")
	// A socket inode without a listener produces ECONNREFUSED.
	c = executor(t, func(Request) string { return "" })
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "refused"))
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	c.Socket = l.Addr().String()
	l.Close()
	err = c.Call(context.Background(), "files.write", nil, nil, nil)
	if !errors.As(err, &dial) {
		t.Fatalf("refused dial must be safe to retry: %v", err)
	}
	c = executor(t, func(Request) string { return "" })
	err = c.Call(context.Background(), "files.write", nil, nil, nil)
	dial = nil
	if errors.As(err, &dial) {
		t.Fatalf("accepted request must never be replayed: %v", err)
	}
	code(t, err, "EXECUTOR_PROTOCOL")
	c = executor(t, func(Request) string { return `{"ok":false,"code":"EXECUTOR_UNAVAILABLE"}` + "\n" })
	err = c.Call(context.Background(), "files.write", nil, nil, nil)
	if errors.As(err, &dial) {
		t.Fatal("executor refusal must not be treated as a dial failure")
	}
	code(t, err, "EXECUTOR_UNAVAILABLE")
}

// mailbox is a package lifecycle mailbox whose last request is action,
// written age ago.
func mailbox(t *testing.T, action string, age time.Duration) string {
	t.Helper()
	dir := t.TempDir()
	control := filepath.Join(dir, "control")
	if err := os.WriteFile(control, []byte(action+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(control, at, at); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestClientTellsAStoppedPackageFromAStartingOne(t *testing.T) {
	missing := func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") }
	t.Run("stopped", func(t *testing.T) {
		c := &Client{Socket: missing(t), Mailbox: mailbox(t, "stop", time.Hour)}
		err := c.Call(context.Background(), "root.run", nil, nil, nil)
		var dial *dialFailure
		if !errors.As(err, &dial) {
			t.Fatalf("a stopped package was sent the request: %v", err)
		}
		code(t, err, CodeAppStopped)
	})
	// the stop text showed 28 s after a stop. While the package
	// stops, its executor can still hold the socket and not answer; a client
	// that dials first waits out its timeout. The mailbox comes first.
	t.Run("stopping while the executor still holds its socket", func(t *testing.T) {
		l, err := net.Listen("unix", filepath.Join(t.TempDir(), "s"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		c := &Client{Socket: l.Addr().String(), Mailbox: mailbox(t, "stop", 0), Timeout: 5 * time.Second}
		begin := time.Now()
		err = c.Call(context.Background(), "status", nil, nil, nil)
		var dial *dialFailure
		if !errors.As(err, &dial) {
			t.Fatalf("a stopping package was sent the request: %v", err)
		}
		code(t, err, CodeAppStopped)
		if took := time.Since(begin); took > 100*time.Millisecond {
			t.Fatalf("a stopping package answered after %v", took)
		}
	})
	t.Run("paused long after its start", func(t *testing.T) {
		c := &Client{Socket: missing(t), Mailbox: mailbox(t, "start", StartGrace+time.Second)}
		begin := time.Now()
		code(t, c.Call(context.Background(), "root.run", nil, nil, nil), CodeExecutorUnavailable)
		if time.Since(begin) > time.Second {
			t.Fatal("a paused executor must not make every call wait")
		}
	})
	t.Run("still starting when the grace ends", func(t *testing.T) {
		c := &Client{Socket: missing(t), Mailbox: mailbox(t, "start", StartGrace-300*time.Millisecond)}
		code(t, c.Call(context.Background(), "root.run", nil, nil, nil), CodeAppStarting)
	})
	t.Run("listens within the grace", func(t *testing.T) {
		c := &Client{Socket: missing(t), Mailbox: mailbox(t, "start", 0), Timeout: 5 * time.Second}
		time.AfterFunc(300*time.Millisecond, func() {
			l, err := net.Listen("unix", c.Socket)
			if err != nil {
				t.Error(err)
				return
			}
			t.Cleanup(func() { l.Close() })
			conn, err := l.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			bufio.NewReader(conn).ReadBytes('\n')
			conn.Write([]byte(`{"ok":true,"result":{}}` + "\n"))
		})
		if err := c.Call(context.Background(), "root.run", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("no mailbox", func(t *testing.T) {
		c := &Client{Socket: missing(t), Mailbox: t.TempDir()}
		code(t, c.Call(context.Background(), "root.run", nil, nil, nil), CodeExecutorUnavailable)
	})
}
