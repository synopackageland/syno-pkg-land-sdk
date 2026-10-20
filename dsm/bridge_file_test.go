package dsm

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestUnlinkedBridgeLeavesOnlyAReadOnlyDescriptor(t *testing.T) {
	dir := t.TempDir()
	file, err := unlinkedBridge(dir, []byte("bridge"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil || string(got) != "bridge" {
		t.Fatalf("payload %q %v", got, err)
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("attack")); err == nil {
		t.Fatal("bridge descriptor is writable")
	}
	info, err := file.Stat()
	if err != nil || info.Mode().Perm() != 0o500 {
		t.Fatalf("mode %v %v", info.Mode(), err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("bridge left a name behind: %v %v", entries, err)
	}
}

func TestUnlinkedBridgeRefusesWithoutAPrivateDirectory(t *testing.T) {
	shared := t.TempDir()
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"unset": "", "relative": "tmp", "missing": filepath.Join(shared, "none"), "shared": shared} {
		if file, err := unlinkedBridge(dir, []byte("bridge")); err == nil {
			file.Close()
			t.Fatalf("%s directory accepted", name)
		}
	}
	// DSM's /var/packages/<pkg>/tmp is a root-owned link to @apptemp/<pkg>.
	file, err := unlinkedBridge(link, []byte("bridge"))
	if err != nil {
		t.Fatalf("linked private directory refused: %v", err)
	}
	file.Close()
}
