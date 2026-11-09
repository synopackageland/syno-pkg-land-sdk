package sharedfolder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSelectedSubpath(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "volume1", "Media")
	os.MkdirAll(filepath.Join(base, "Projects", "Level two", "資料"), 0755)
	got, err := ResolveSubpath(root, "Media", "Projects/Level two/資料")
	if err != nil || got != filepath.Join(base, "Projects", "Level two", "資料") {
		t.Fatalf("%q %v", got, err)
	}
	os.Symlink(filepath.Join(base, "Projects"), filepath.Join(base, "link"))
	for _, p := range []string{"../other", "Projects//child", "link/Level two", "missing", "/absolute", "Projects/.", "a\\b"} {
		if _, err := ResolveSubpath(root, "Media", p); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
}
func TestSubpathsRejectMissingCorruptAndTraversal(t *testing.T) {
	dir := t.TempDir()
	if _, err := Subpaths(dir); err == nil {
		t.Fatal("missing state accepted")
	}
	for _, raw := range []string{`{`, `{"schema":2}`, `{"schema":1,"shared_folder_paths":{"media":"../other"}}`} {
		os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0600)
		if _, err := Subpaths(dir); err == nil {
			t.Fatal(raw)
		}
	}
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"schema":1,"shared_folder_paths":{"media":""}}`), 0600)
	paths, err := Subpaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := paths["media"]; !ok || p != "" {
		t.Fatal(paths)
	}
}
