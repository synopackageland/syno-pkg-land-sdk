package conformance

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The Go client's generated constants and test vectors must equal what
// go generate makes from this spec.
func TestGeneratedClientFilesAreCurrent(t *testing.T) {
	spec, err := filepath.Abs(specDir)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	cmd := exec.Command("go", "run", "./internal/specgen", spec, out)
	cmd.Dir = ".."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("specgen: %v\n%s", err, output)
	}
	committed := "../delegate"
	var files []string
	filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(out, path)
			files = append(files, rel)
		}
		return err
	})
	for _, rel := range files {
		want, _ := os.ReadFile(filepath.Join(out, rel))
		got, err := os.ReadFile(filepath.Join(committed, rel))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("delegate/%s is stale; run go generate ./... at the repository root", rel)
		}
	}
	stale, _ := filepath.Glob(filepath.Join(committed, "testdata", "vectors", "*"))
	if len(stale) != len(files)-1 {
		t.Errorf("delegate/testdata/vectors has %d files, spec has %d", len(stale), len(files)-1)
	}
}

// The bridge accepts the catalog's upper-case refusals from an App.
func TestGeneratedBridgeRefusalsAreCurrent(t *testing.T) {
	checkGenerated(t, "-bridge", "refusals_gen.go", "bridge/refusals_gen.go")
}

// The Rust App SDK names the same refusals.
func TestGeneratedRustRefusalsAreCurrent(t *testing.T) {
	checkGenerated(t, "-rust", "refusals.rs", "rust/app/src/refusals.rs")
}

// The Python App SDK accepts the same refusals.
func TestGeneratedPythonRefusalsAreCurrent(t *testing.T) {
	checkGenerated(t, "-python", "_refusals.py", "python/synopkgland_app/_refusals.py")
}

// checkGenerated runs specgen with flag and compares its file with the
// committed one, relative to the repository root.
func checkGenerated(t *testing.T, flag, file, committed string) {
	t.Helper()
	spec, err := filepath.Abs(specDir)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	cmd := exec.Command("go", "run", "./internal/specgen", flag, spec, out)
	cmd.Dir = ".."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("specgen %s: %v\n%s", flag, err, output)
	}
	want, err := os.ReadFile(filepath.Join(out, file))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", committed))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("%s is stale; run go generate ./... at the repository root", committed)
	}
}
