//go:build unix

package bridge

import (
	"encoding/json"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// The Python App SDK turns the runtime's reserved codes into internal itself,
// so an App's log says why; its set must be this runtime's.
func TestPythonSDKReservesTheRuntimeCodes(t *testing.T) {
	cmd := exec.Command(interpreter(t, "python3"), "-B", "-c", "import json, synopkgland_app as s; print(json.dumps(sorted(s._RESERVED)))")
	cmd.Dir = filepath.Join(root(t), "python")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if want := slices.Sorted(maps.Keys(reservedCodes)); !slices.Equal(got, want) {
		t.Errorf("python/synopkgland_app _RESERVED %v, runtime %v", got, want)
	}
}
