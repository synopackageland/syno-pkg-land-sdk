package delegate

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func approvalClientFixture(t *testing.T, changed bool) *Client {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root socket-directory fixture")
	}
	const base = "/run/synopkgland"
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "ApprovalFixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	old, next := strings.Repeat("a", 64), strings.Repeat("a", 64)
	permissions := "unchanged"
	changes := []any{}
	if changed {
		next, permissions = strings.Repeat("b", 64), "review-required"
		changes = append(changes, map[string]any{"path": "/scope", "old": "home", "new": "install-selected"})
	}
	b, err := json.Marshal(map[string]any{
		"package": filepath.Base(dir), "old_policy_digest": old, "new_policy_digest": next,
		"new_executor_digest": strings.Repeat("c", 64), "permissions": permissions,
		"task_template_changed": false, "root_programs": []any{},
		"policy_changes": changes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "approval.json"), b, 0640); err != nil {
		t.Fatal(err)
	}
	c := Local(filepath.Base(dir))
	c.Mailbox = mailbox(t, "start", 0)
	c.Timeout = time.Second
	return c
}

func TestApprovalPendingDoesNotWaitForAStartingExecutor(t *testing.T) {
	c := approvalClientFixture(t, true)
	for _, op := range []string{"status", "root.run"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		begin := time.Now()
		code(t, c.Call(ctx, op, nil, nil, nil), CodeExecutorUnavailable)
		cancel()
		if time.Since(begin) > 250*time.Millisecond {
			t.Fatal("approval pending waited for executor startup")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got := (&Service{Client: c}).Status(ctx, nil); got.State != "needs-admin-approval" {
		t.Fatalf("status: %+v", got)
	}
}

func TestApprovalPendingStatusKeepsComparisonWithAFreshStart(t *testing.T) {
	c := approvalClientFixture(t, true)
	data := t.TempDir()
	// Route and transport may receive the lifecycle mailbox independently.
	if err := os.MkdirAll(filepath.Join(data, "executor"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "executor", "control"), []byte("start"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, transportMailbox := range []string{c.Mailbox, ""} {
		c.Mailbox = transportMailbox
		for _, who := range []api.Identity{executorAdmin, executorAlice} {
			out := executorCall(t, c, data, who, "executor.status", `{}`)
			status, _ := out["data"].(map[string]any)
			if out["success"] != true || status["ready"] != false || status["state"] != "paused" {
				t.Fatal(out)
			}
			diff, found := status["approval"].(map[string]any)
			if found != who.Admin {
				t.Fatal("diagnostic audience", out)
			}
			if who.Admin && (diff["permissions"] != "review-required" || diff["old_policy_digest"] != strings.Repeat("a", 64) || diff["new_policy_digest"] != strings.Repeat("b", 64) || len(diff["policy_changes"].([]any)) != 1) {
				t.Fatal("comparison lost", out)
			}
		}
	}
	c.Mailbox = mailbox(t, "stop", 0)
	code(t, c.Call(context.Background(), "status", nil, nil, nil), CodeAppStopped)
}

func TestUnchangedApprovalReadbackStillWaitsForExecutorStart(t *testing.T) {
	c := approvalClientFixture(t, false)
	c.Timeout = time.Second
	time.AfterFunc(300*time.Millisecond, func() {
		l, err := net.Listen("unix", c.Socket)
		if err != nil {
			t.Error(err)
			return
		}
		defer l.Close()
		conn, err := l.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		var request Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		conn.Write([]byte("{\"ok\":true,\"result\":{}}\n"))
	})
	if err := c.Call(context.Background(), "status", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestListeningExecutorTakesPrecedenceOverApprovalDiagnostics(t *testing.T) {
	c := approvalClientFixture(t, true)
	l, err := net.Listen("unix", c.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		conn.Write([]byte("{\"ok\":true,\"result\":{\"ready\":true}}\n"))
	}()
	var status ExecutorStatus
	if err := c.Call(context.Background(), "status", nil, nil, &status); err != nil || !status.Ready {
		t.Fatalf("listening executor: %+v, %v", status, err)
	}
	<-done
}

func TestApprovalReadbackAppearingDuringStartEndsTheWait(t *testing.T) {
	c := approvalClientFixture(t, true)
	path := filepath.Join(filepath.Dir(c.Socket), "approval.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(300*time.Millisecond, func() {
		if err := os.WriteFile(path, b, 0640); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	code(t, c.Call(ctx, "status", nil, nil, nil), CodeExecutorUnavailable)
}

func TestProtectedApprovalRejectsWritableSymlinkAndWrongPackage(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root ownership fixture")
	}
	dir := t.TempDir()
	name := filepath.Join(dir, "approval.json")
	b := []byte(`{"package":"Example","old_policy_digest":"` + strings.Repeat("a", 64) + `","new_policy_digest":"` + strings.Repeat("b", 64) + `","new_executor_digest":"` + strings.Repeat("c", 64) + `"}`)
	os.WriteFile(name, b, 0640)
	if readProtectedApproval(dir, "Example") == nil {
		t.Fatal("root readback refused")
	}
	if readProtectedApproval(dir, "Other") != nil {
		t.Fatal("wrong package accepted")
	}
	os.Chmod(name, 0660)
	if readProtectedApproval(dir, "Example") != nil {
		t.Fatal("writable file accepted")
	}
	os.Chmod(name, 0640)
	os.Rename(name, name+".real")
	os.Symlink(name+".real", name)
	if readProtectedApproval(dir, "Example") != nil {
		t.Fatal("symlink accepted")
	}
}

func TestExecutorStatusReturnsRootDiagnosticsOnlyToAdministrator(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root socket-directory fixture")
	}
	dir := "/run/synopkgland/T0068ApprovalFixture"
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Skip("fixture already exists")
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	b := []byte(`{"package":"T0068ApprovalFixture","old_policy_digest":"` + strings.Repeat("a", 64) + `","new_policy_digest":"` + strings.Repeat("b", 64) + `","new_executor_digest":"` + strings.Repeat("c", 64) + `","permissions":"unchanged"}`)
	if err := os.WriteFile(filepath.Join(dir, "approval.json"), b, 0640); err != nil {
		t.Fatal(err)
	}
	c := Local("T0068ApprovalFixture")
	for _, who := range []api.Identity{executorAdmin, executorAlice} {
		out := executorCall(t, c, t.TempDir(), who, "executor.status", `{}`)
		data, _ := out["data"].(map[string]any)
		if out["success"] != true || data == nil {
			t.Fatal(out)
		}
		_, found := data["approval"]
		if found != who.Admin {
			t.Fatal("diagnostic audience", out)
		}
	}
}
