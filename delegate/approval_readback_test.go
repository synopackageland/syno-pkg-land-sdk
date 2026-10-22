package delegate

import (
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
