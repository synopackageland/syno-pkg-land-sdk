//go:build unix

package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary doubles as an exec-mode App: run with helperArg, it answers
// one call on stdin/stdout with its own environment. With stallArg it says
// hello, starts a child in its process group, writes the child's PID to
// $STALL_PID_FILE and then neither reads nor answers.
const (
	helperArg = "bridge-helper-app"
	stallArg  = "bridge-stall-app"
)

func TestMain(m *testing.M) {
	if slices.Contains(os.Args, helperArg) || os.Getenv("BRIDGE_TEST_APP") == "env" {
		helperApp()
		return
	}
	if slices.Contains(os.Args, stallArg) || os.Getenv("BRIDGE_TEST_APP") == "stall" {
		stallApp()
		return
	}
	os.Exit(m.Run())
}

func stallApp() {
	bufio.NewReader(os.Stdin).ReadBytes('\n')
	os.Stdout.WriteString(`{"t":"hello","version":1,"sdk":"go-test/0","methods":["a.b"]}` + "\n")
	child := exec.Command("/bin/sleep", "60")
	child.Start()
	os.WriteFile(os.Getenv("STALL_PID_FILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o600)
	time.Sleep(time.Hour)
}

func helperApp() {
	in, out := bufio.NewReader(os.Stdin), os.Stdout
	line := func() map[string]any {
		b, _ := in.ReadBytes('\n')
		var m map[string]any
		json.Unmarshal(b, &m)
		return m
	}
	send := func(v any) { b, _ := json.Marshal(v); out.Write(append(b, '\n')) }
	line()
	send(map[string]any{"t": "hello", "version": 1, "sdk": "go-test/0", "methods": []string{"env.dump"}})
	line()
	send(map[string]any{"t": "result", "ok": true, "data": os.Environ()})
}

func TestExecRunsTheAppWithOnlyTheGivenEnvironment(t *testing.T) {
	t.Setenv("HTTP_COOKIE", "id=secret")
	t.Setenv("HTTP_X_SYNO_TOKEN", "token")
	e := &Exec{Path: os.Args[0], Args: []string{helperArg}, Env: []string{"LANG=C.UTF-8"}}
	s := &Session{Dial: e, Methods: []string{"env.dump"}}
	data, err := s.Call(context.Background(), "env.dump", nil, &Call{Context: alice})
	if err != nil {
		t.Fatal(err)
	}
	var env []string
	json.Unmarshal(data, &env)
	slices.Sort(env)
	if !slices.Equal(env, []string{"LANG=C.UTF-8", "SYNOPKGLAND_BRIDGE=stdio"}) {
		t.Fatalf("env %v", env)
	}
}

func TestExecRefusesToRunAsRoot(t *testing.T) {
	defer func(f func() int) { geteuid = f }(geteuid)
	geteuid = func() int { return 0 }
	if _, err := (&Exec{Path: os.Args[0]}).Dial(context.Background()); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("err %v", err)
	}
}

func TestExecReportsAMissingOrCrashingAppAsUnavailable(t *testing.T) {
	for name, e := range map[string]*Exec{
		"missing": {Path: filepath.Join(t.TempDir(), "missing")},
		"crash":   {Path: "/bin/sh", Args: []string{"-c", "exit 3"}},
	} {
		s := &Session{Dial: e, Methods: []string{"a.b"}}
		if _, err := s.Call(context.Background(), "a.b", nil, &Call{Context: alice}); !isUnavailable(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// At the deadline the runtime stops writing to an App that does not read,
// and kills the App's whole process group.
func TestDeadlineEndsTheAppAndWhatItStarted(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	e := &Exec{Path: os.Args[0], Args: []string{stallArg}, Env: []string{"STALL_PID_FILE=" + pidFile}}
	s := &Session{Dial: e, Methods: []string{"a.b"}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	params := json.RawMessage(`"` + strings.Repeat("x", 4<<20) + `"`)
	done := make(chan error, 1)
	go func() { _, err := s.Call(ctx, "a.b", params, &Call{Context: alice}); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call outlived its deadline")
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(raw))
	for start := time.Now(); syscall.Kill(pid, 0) == nil; time.Sleep(10 * time.Millisecond) {
		if time.Since(start) > 2*time.Second {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the App's child %d outlived the call", pid)
		}
	}
}

// timerLate is a context whose deadline has passed but whose timer has not
// fired yet: the write deadline can end a write first.
type timerLate struct{ context.Context }

func (timerLate) Deadline() (time.Time, bool) { return time.Now().Add(-time.Millisecond), true }
func (timerLate) Err() error                  { return nil }

func TestAWriteThatHitsTheRequestDeadlineIsATimeout(t *testing.T) {
	if err := writeFailed(timerLate{context.Background()}, os.ErrDeadlineExceeded); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if err := writeFailed(context.Background(), syscall.EPIPE); !errors.Is(err, errGone) {
		t.Fatalf("err %v", err)
	}
}
