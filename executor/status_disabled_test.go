package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStatusReadsProtectedDisabledAndRefusesStoreError(t *testing.T) {
	e, _, _ := setup(t)
	check := func(ready, disabled bool) {
		t.Helper()
		r := request(e, "status", map[string]any{})
		b, _ := json.Marshal(r.Result)
		var out struct {
			Ready    bool  `json:"ready"`
			Disabled *bool `json:"disabled"`
		}
		if !r.OK || json.Unmarshal(b, &out) != nil || out.Ready != ready || out.Disabled == nil || *out.Disabled != disabled {
			t.Fatalf("status: %s", b)
		}
	}
	check(true, false)
	if err := e.Store.SetDisabled(true); err != nil {
		t.Fatal(err)
	}
	check(true, true)
	if err := os.WriteFile(filepath.Join(e.Store.Dir, "disabled.record"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	check(false, false)
}
