package composeimages

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const digest = "@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func lock(t *testing.T, refs ...string) string {
	t.Helper()
	images := []map[string]any{}
	for _, ref := range refs {
		images = append(images, map[string]any{"service": "app", "reference": ref, "pinned": true})
	}
	raw, _ := json.Marshal(map[string]any{"schema": 1, "package": "Fixture", "images": images})
	path := filepath.Join(t.TempDir(), "compose.lock.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRememberRecordsOnlyPinnedLockedImages(t *testing.T) {
	dir := t.TempDir()
	if err := Remember(lock(t, "example/app"+digest, "example/db"+digest), dir, "Fixture"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("record: %v %v", st, err)
	}
	got := Remembered(dir, "Fixture")
	if !got["example/app"+digest] || !got["example/db"+digest] || len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if len(Remembered(dir, "Other")) != 0 {
		t.Fatal("record of another package accepted")
	}
	if err := Remember(lock(t, "example/app:latest"), dir, "Fixture"); err == nil {
		t.Fatal("mutable reference recorded")
	}
}

func TestRememberedIgnoresForeignOrMissingRecords(t *testing.T) {
	dir := t.TempDir()
	if len(Remembered(dir, "Fixture")) != 0 {
		t.Fatal("missing record")
	}
	os.WriteFile(filepath.Join(dir, FileName), []byte(`{"schema":1,"package":"Fixture","images":["example/app`+digest+`"]}`), 0644)
	if len(Remembered(dir, "Fixture")) != 0 {
		t.Fatal("non-private record trusted")
	}
}
