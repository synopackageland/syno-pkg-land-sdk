package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// An install without tasks.json is cleaned with the inventory of its approved
// policy.json in the root-owned runtime.
func TestPinnedInventoryUsesTheApprovedPolicy(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root: the runtime must be root-owned")
	}
	runtime := filepath.Join(t.TempDir(), "runtime")
	os.Mkdir(runtime, 0700)
	policy := []byte(`{"v":1,"package":"TestApp"}`)
	os.WriteFile(filepath.Join(runtime, "policy.json"), policy, 0600)
	inv, err := pinnedInventory(context.Background(), "TestApp", runtime)
	if err != nil || inv.PolicyDigest != hashBytes(policy) || !validInventory("TestApp", inv) {
		t.Fatal(inv, err)
	}
	os.Chmod(filepath.Join(runtime, "policy.json"), 0666)
	if _, err = pinnedInventory(context.Background(), "TestApp", runtime); err == nil {
		t.Fatal("writable policy trusted")
	}
}
