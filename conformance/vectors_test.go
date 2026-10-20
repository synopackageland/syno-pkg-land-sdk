// Package conformance runs spec/protocol/v1 against the executor over a real
// Unix socket. It is a repository check, never a published module.
package conformance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const specDir = "../spec/protocol/v1"

var server string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "conformance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server = filepath.Join(dir, "contract-server")
	build := exec.Command("go", "test", "-tags", "contractserver", "-c", "-o", server, ".")
	build.Dir = "../executor"
	if output, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		fmt.Fprintf(os.Stderr, "compile executor: %v\n%s", err, output)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type vector struct {
	Name    string          `json:"name"`
	Request json.RawMessage `json:"request"`
	Policy  json.RawMessage `json:"policy"`
	State   json.RawMessage `json:"state"`
	Expect  json.RawMessage `json:"expect"`
}

func loadVectors(t *testing.T) map[string]vector {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(specDir, "vectors", "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no vectors: %v", err)
	}
	out := map[string]vector{}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
			var v vector
			d := json.NewDecoder(bytes.NewReader(line))
			d.DisallowUnknownFields()
			if err := d.Decode(&v); err != nil {
				t.Fatalf("%s:%d: %v", file, i+1, err)
			}
			if v.Name == "" || v.Request == nil || v.Policy == nil || v.State == nil || v.Expect == nil {
				t.Fatalf("%s:%d: name, request, policy, state and expect are required", file, i+1)
			}
			key := strings.TrimSuffix(filepath.Base(file), ".jsonl") + "/" + v.Name
			if _, dup := out[key]; dup {
				t.Fatalf("duplicate vector %s", key)
			}
			out[key] = v
		}
	}
	return out
}

// frame is the request line: a JSON string is sent as raw bytes, so vectors
// can carry malformed requests; an object is sent as compact JSON.
func frame(raw json.RawMessage) ([]byte, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []byte(s + "\n"), nil
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return nil, err
	}
	return append(b.Bytes(), '\n'), nil
}

func TestVectors(t *testing.T) {
	for name, v := range loadVectors(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := exchange(t, v)
			var want, have any
			if err := json.Unmarshal(v.Expect, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(got, &have); err != nil {
				t.Fatalf("reply %s: %v", got, err)
			}
			if !match(want, have) {
				t.Fatalf("expect %s\n got   %s", v.Expect, got)
			}
		})
	}
}

func exchange(t *testing.T, v vector) []byte {
	t.Helper()
	socket, stderr := startExecutor(t, v)
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	line, err := frame(v.Request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write(line); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read reply: %v (%s)", err, stderr.Bytes())
	}
	return reply
}

// startExecutor runs the contract executor with the vector's policy and
// state until the test ends, and returns its socket once it accepts.
func startExecutor(t *testing.T, v vector) (string, *bytes.Buffer) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cv-") // Unix socket paths are short.
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	// Grants bind real directories; a symlinked temp root (macOS /var) is refused.
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"policy.json": v.Policy, "state.json": v.State} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, server, "-test.run=^TestContractServer$", "-test.timeout=1m", "-test.v")
	cmd.Env = append(os.Environ(), "CONTRACT_DIR="+dir)
	stderr := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = stderr, stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	t.Cleanup(func() { cmd.Process.Kill(); <-exited })
	socket := filepath.Join(dir, "executor.sock")
	for {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			conn.Close()
			return socket, stderr
		}
		select {
		case <-exited:
			t.Fatalf("executor exited: %s", stderr.Bytes())
		case <-ctx.Done():
			t.Fatalf("executor did not start: %s", stderr.Bytes())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// match reports whether have satisfies want: objects match on the keys want
// lists, "<any>" matches any present value, everything else is equal.
func match(want, have any) bool {
	if want == "<any>" {
		return have != nil
	}
	switch w := want.(type) {
	case map[string]any:
		h, ok := have.(map[string]any)
		if !ok {
			return false
		}
		for k, wv := range w {
			hv, present := h[k]
			if !present || !match(wv, hv) {
				return false
			}
		}
		return true
	case []any:
		h, ok := have.([]any)
		if !ok || len(h) != len(w) {
			return false
		}
		for i := range w {
			if !match(w[i], h[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(want, have)
}
