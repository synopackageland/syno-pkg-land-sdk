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

func TestSelectedMountMetadataRejectsKnownUnsupportedTypes(t *testing.T) {
	for _, kind := range []string{"nfs", "nfs4", "cifs", "smb3", "ceph", "fuse.sshfs"} {
		raw := "1 0 8:1 / / rw - ext4 /dev/root rw\n2 1 0:3 / /volume1/Media/Remote rw - " + kind + " remote rw\n"
		if err := validateMountInfo("/volume1/Media/Remote/Child", raw); err == nil {
			t.Fatal(kind)
		}
	}
	for _, opts := range []string{"ro", "ro,nosuid"} {
		if err := validateMountInfo("/volume1/Media", "1 0 8:1 / / "+opts+" - btrfs /dev/root rw"); err == nil {
			t.Fatal(opts)
		}
	}
	if err := validateMountInfo("/volume1/Media/Child", "1 0 8:1 / / rw - btrfs /dev/root rw"); err != nil {
		t.Fatal(err)
	}
}

func TestStatusAndRequireFollowInstallerChildSelectionWithoutAppChanges(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	child := filepath.Join(root, "volume1", "Media", "Projects", "Deep")
	os.MkdirAll(child, 0700)
	os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"schema":1,"shared_folders":{"media":"Media"},"shared_folder_paths":{"media":"Projects/Deep"}}`), 0600)
	cfg := Config{DataDir: data, Root: root, Required: map[string]Access{"media": Write}}
	path, err := cfg.Require("media")
	if err != nil || path != child {
		t.Fatalf("%q %v", path, err)
	}
	os.Chmod(child, 0500)
	if os.Geteuid() != 0 {
		status, err := cfg.Status()
		if err != nil || len(status.Folders) != 1 || status.Folders[0].State != Pending || status.Folders[0].Subpath != "Projects/Deep" {
			t.Fatalf("%+v %v", status, err)
		}
		if _, err = cfg.Require("media"); err == nil {
			t.Fatal("write denial lost")
		}
	}
}

func TestLegacyShareOnlySelectionMeansRootButMalformedPairsDoNot(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"schema":1,"shared_folders":{"media":"Media"}}`), 0600)
	paths, err := Subpaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := paths["media"]; !ok || p != "" {
		t.Fatalf("legacy root lost: %+v", paths)
	}
	for _, raw := range []string{
		`{"schema":1,"shared_folders":{"media":"Media"},"shared_folder_paths":null}`,
		`{"schema":1,"shared_folders":{"media":"Media"},"shared_folder_paths":{}}`,
		`{"schema":1,"shared_folders":{"media":"Media"},"shared_folder_paths":{"media":"../escape"}}`,
	} {
		os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0600)
		if _, err := Subpaths(dir); err == nil {
			t.Fatalf("malformed pair accepted: %s", raw)
		}
	}
}
