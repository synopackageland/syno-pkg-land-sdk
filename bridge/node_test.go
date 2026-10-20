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

// The Node App SDK refuses the runtime's reserved codes in AppError, so the
// App learns it at once; its set must be this runtime's. node/dist is the
// built package (npm run build in node/).
func TestNodeSDKReservesTheRuntimeCodes(t *testing.T) {
	cmd := exec.Command(interpreter(t, "node"), "--input-type=module", "-e", `import { reservedCodes } from './dist/errors.js'; console.log(JSON.stringify([...reservedCodes].sort()))`)
	cmd.Dir = filepath.Join(root(t), "node")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if want := slices.Sorted(maps.Keys(reservedCodes)); !slices.Equal(got, want) {
		t.Errorf("node/src/errors.ts reservedCodes %v, runtime %v", got, want)
	}
}
