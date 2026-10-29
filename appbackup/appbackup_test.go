package appbackup

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func cfg() Config { return Config{Schema: 1, Package: "demo", DataVersion: "1.2"} }

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		switch {
		case d.Type()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[rel] = "-> " + target
		case d.IsDir():
			out[rel] = "/"
		default:
			b, _ := os.ReadFile(p)
			out[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func entries(t *testing.T, archive []byte) []string {
	t.Helper()
	var names []string
	r := tar.NewReader(bytes.NewReader(archive))
	for {
		h, err := r.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
}

func TestConfigValidate(t *testing.T) {
	good := cfg()
	if err := good.Validate(); err != nil {
		t.Fatalf("valid config refused: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"schema":       func(c *Config) { c.Schema = 2 },
		"package":      func(c *Config) { c.Package = "../x" },
		"version":      func(c *Config) { c.DataVersion = "1" },
		"version-text": func(c *Config) { c.DataVersion = "1.x" },
		"hook-abs":     func(c *Config) { c.Hook = "/bin/sh" },
		"hook-parent":  func(c *Config) { c.Hook = "../bin/hook" },
	} {
		c := cfg()
		mutate(&c)
		if err := c.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestExportImportRestoresTheWholeDataDirectory(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "app.json"), `{"a":1}`)
	write(t, filepath.Join(src, "nested/deep/file.txt"), "deep")
	if err := os.Mkdir(filepath.Join(src, "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("app.json", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := Export(src, &archive, cfg(), Options{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if names := entries(t, archive.Bytes()); names[0] != HeaderName {
		t.Fatalf("first entry = %q, want header", names[0])
	}

	dst := t.TempDir()
	write(t, filepath.Join(dst, "stale.txt"), "remove me")
	write(t, filepath.Join(dst, "app.json"), `{"a":2}`)
	if err := Import(dst, &archive, cfg(), Options{}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got, want := tree(t, dst), tree(t, src); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored tree = %v, want %v", got, want)
	}
	if info, err := os.Stat(filepath.Join(dst, "app.json")); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("restored mode = %v, %v", info.Mode(), err)
	}
}

func TestExportSkipsSocketsAndStagingDirectories(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "keep"), "x")
	write(t, filepath.Join(src, stagingPrefix+"old/file"), "stale")
	var archive bytes.Buffer
	if err := Export(src, &archive, cfg(), Options{}); err != nil {
		t.Fatal(err)
	}
	names := entries(t, archive.Bytes())
	if want := []string{HeaderName, "data/keep"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	if _, err := os.Stat(filepath.Join(src, stagingPrefix+"old")); !os.IsNotExist(err) {
		t.Fatalf("stale staging directory kept: %v", err)
	}
}

func TestOnlineExportSnapshotsSQLiteAndSkipsItsJournals(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "db.sqlite"), "SQLite format 3\x00live pages")
	write(t, filepath.Join(src, "db.sqlite-wal"), "wal")
	write(t, filepath.Join(src, "db.sqlite-shm"), "shm")
	write(t, filepath.Join(src, "notes.txt"), "plain")
	var snapped []string
	opts := Options{SnapshotSQLite: func(from, to string) error {
		snapped = append(snapped, filepath.Base(from))
		return os.WriteFile(to, []byte("SQLite format 3\x00snapshot"), 0o600)
	}}
	c := cfg()
	c.Online = true
	var archive bytes.Buffer
	if err := Export(src, &archive, c, opts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapped, []string{"db.sqlite"}) {
		t.Fatalf("snapshots = %v", snapped)
	}
	names := entries(t, archive.Bytes())
	sort.Strings(names)
	if want := []string{"data/db.sqlite", "data/notes.txt", HeaderName}; !reflect.DeepEqual(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	dst := t.TempDir()
	if err := Import(dst, &archive, c, Options{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "db.sqlite")); string(b) != "SQLite format 3\x00snapshot" {
		t.Fatalf("restored database = %q, want the snapshot", b)
	}
}

func TestOfflineExportKeepsSQLiteJournalsVerbatim(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "db.sqlite"), "SQLite format 3\x00pages")
	write(t, filepath.Join(src, "db.sqlite-wal"), "wal")
	opts := Options{SnapshotSQLite: func(string, string) error { t.Fatal("offline export must not snapshot"); return nil }}
	var archive bytes.Buffer
	if err := Export(src, &archive, cfg(), opts); err != nil {
		t.Fatal(err)
	}
	names := entries(t, archive.Bytes())
	sort.Strings(names)
	if want := []string{"data/db.sqlite", "data/db.sqlite-wal", HeaderName}; !reflect.DeepEqual(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
}

func TestSQLiteSnapshotWithTheSystemTool(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not installed")
	}
	src := t.TempDir()
	db := filepath.Join(src, "app.db")
	if out, err := exec.Command(sqlite, db, "PRAGMA journal_mode=WAL; CREATE TABLE t(v); INSERT INTO t VALUES('kept');").CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	c := cfg()
	c.Online = true
	var archive bytes.Buffer
	if err := Export(src, &archive, c, Options{SQLite: sqlite}); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := Import(dst, &archive, c, Options{}); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(sqlite, filepath.Join(dst, "app.db"), "SELECT v FROM t;").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "kept" {
		t.Fatalf("restored rows = %q, %v", out, err)
	}
}

func TestHookFreezesAndThawsAroundExport(t *testing.T) {
	src, target := t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "hook.log")
	write(t, filepath.Join(target, "bin/hook"), "#!/bin/sh\necho \"$1\" >> '"+log+"'\n")
	if err := os.Chmod(filepath.Join(target, "bin/hook"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := cfg()
	c.Hook = "bin/hook"
	if err := Export(src, io.Discard, c, Options{Target: target}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); string(b) != "freeze\nthaw\n" {
		t.Fatalf("hook calls = %q", b)
	}
}

func TestFailedFreezeStopsExport(t *testing.T) {
	src, target := t.TempDir(), t.TempDir()
	write(t, filepath.Join(target, "hook"), "#!/bin/sh\n[ \"$1\" = freeze ] && exit 3\nexit 0\n")
	os.Chmod(filepath.Join(target, "hook"), 0o755)
	c := cfg()
	c.Hook = "hook"
	if err := Export(src, io.Discard, c, Options{Target: target}); err == nil {
		t.Fatal("export continued after a failed freeze")
	}
}

func archiveWith(t *testing.T, header any, files map[string]*tar.Header) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	raw, _ := json.Marshal(header)
	w.WriteHeader(&tar.Header{Name: HeaderName, Mode: 0o600, Size: int64(len(raw)), Typeflag: tar.TypeReg})
	w.Write(raw)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := files[n]
		h.Name = n
		w.WriteHeader(h)
		if h.Typeflag == tar.TypeReg {
			w.Write(make([]byte, h.Size))
		}
	}
	w.Close()
	return &b
}

func TestImportChecksPackageAndDataVersion(t *testing.T) {
	for name, tc := range map[string]struct {
		header Header
		ok     bool
	}{
		"same":          {Header{Format: 1, Package: "demo", DataVersion: "1.2"}, true},
		"older-minor":   {Header{Format: 1, Package: "demo", DataVersion: "1.0"}, true},
		"newer-minor":   {Header{Format: 1, Package: "demo", DataVersion: "1.3"}, false},
		"other-major":   {Header{Format: 1, Package: "demo", DataVersion: "2.0"}, false},
		"other-package": {Header{Format: 1, Package: "other", DataVersion: "1.2"}, false},
		"other-format":  {Header{Format: 9, Package: "demo", DataVersion: "1.2"}, false},
	} {
		dst := t.TempDir()
		write(t, filepath.Join(dst, "keep"), "original")
		err := Import(dst, archiveWith(t, tc.header, nil), cfg(), Options{})
		if tc.ok != (err == nil) {
			t.Errorf("%s: err = %v", name, err)
		}
		if !tc.ok {
			if !errors.Is(err, ErrIncompatible) {
				t.Errorf("%s: err = %v, want ErrIncompatible", name, err)
			}
			if b, _ := os.ReadFile(filepath.Join(dst, "keep")); string(b) != "original" {
				t.Errorf("%s: refused import changed data", name)
			}
		}
	}
}

func TestImportRefusesUnsafeEntriesAndKeepsData(t *testing.T) {
	h := Header{Format: 1, Package: "demo", DataVersion: "1.2"}
	for name, files := range map[string]map[string]*tar.Header{
		"parent":        {"data/../evil": {Typeflag: tar.TypeReg, Mode: 0o644}},
		"absolute":      {"/etc/evil": {Typeflag: tar.TypeReg, Mode: 0o644}},
		"outside-data":  {"other/file": {Typeflag: tar.TypeReg, Mode: 0o644}},
		"hardlink":      {"data/h": {Typeflag: tar.TypeLink, Linkname: "data/x", Mode: 0o644}},
		"device":        {"data/d": {Typeflag: tar.TypeChar, Mode: 0o644}},
		"through-link":  {"data/l": {Typeflag: tar.TypeSymlink, Linkname: "/tmp"}, "data/l/x": {Typeflag: tar.TypeReg, Mode: 0o644}},
		"duplicate-hdr": {HeaderName: {Typeflag: tar.TypeReg, Mode: 0o644}},
	} {
		dst := t.TempDir()
		write(t, filepath.Join(dst, "keep"), "original")
		if err := Import(dst, archiveWith(t, h, files), cfg(), Options{}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
		if got := tree(t, dst); !reflect.DeepEqual(got, map[string]string{"keep": "original"}) {
			t.Errorf("%s: data changed to %v", name, got)
		}
	}
}

func TestImportDropsSetIDBits(t *testing.T) {
	h := Header{Format: 1, Package: "demo", DataVersion: "1.2"}
	dst := t.TempDir()
	if err := Import(dst, archiveWith(t, h, map[string]*tar.Header{"data/x": {Typeflag: tar.TypeReg, Mode: 0o6755}}), cfg(), Options{}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(dst, "x")); info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		t.Fatalf("mode = %v", info.Mode())
	}
}

func TestCompatible(t *testing.T) {
	for _, tc := range []struct {
		backup, current string
		ok              bool
	}{{"1.0", "1.0", true}, {"1.0", "1.9", true}, {"1.10", "1.9", false}, {"2.0", "1.0", false}, {"x", "1.0", false}} {
		if err := Compatible(tc.backup, tc.current); (err == nil) != tc.ok {
			t.Errorf("Compatible(%s, %s) = %v", tc.backup, tc.current, err)
		}
	}
}

func TestExportAndImportFollowASymlinkedDataDirectory(t *testing.T) {
	real := t.TempDir()
	write(t, filepath.Join(real, "data.txt"), "kept")
	link := filepath.Join(t.TempDir(), "var")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := Export(link, &archive, cfg(), Options{}); err != nil {
		t.Fatal(err)
	}
	if names := entries(t, archive.Bytes()); !reflect.DeepEqual(names, []string{HeaderName, "data/data.txt"}) {
		t.Fatalf("entries = %v", names)
	}
	write(t, filepath.Join(real, "data.txt"), "changed")
	if err := Import(link, &archive, cfg(), Options{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(real, "data.txt")); string(b) != "kept" {
		t.Fatalf("restored = %q", b)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("data directory link replaced: %v %v", info, err)
	}
}
