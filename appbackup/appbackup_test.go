package appbackup

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
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
		"exclude-abs":  func(c *Config) { c.Exclude = []string{"/tmp"} },
		"exclude-up":   func(c *Config) { c.Exclude = []string{"../x"} },
		"exclude-dot":  func(c *Config) { c.Exclude = []string{"a/./b"} },
		"exclude-set":  func(c *Config) { c.Exclude = []string{"[ab]"} },
		"exclude-tail": func(c *Config) { c.Exclude = []string{"tmp/"} },
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

func sqlite3Tool(t *testing.T) string {
	t.Helper()
	tool, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not installed")
	}
	return tool
}

func query(t *testing.T, tool, db, sql string) string {
	t.Helper()
	out, err := exec.Command(tool, "-bail", db, sql).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v %s", sql, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestOnlineExportArchivesSQLiteAndItsWALButNotTheIndex(t *testing.T) {
	tool := sqlite3Tool(t)
	src := t.TempDir()
	db := filepath.Join(src, "app.db")
	query(t, tool, db, "PRAGMA journal_mode=WAL; CREATE TABLE t(v); INSERT INTO t VALUES('kept');")
	// An open writer keeps the WAL and its index next to the database.
	w := exec.Command(tool, db)
	in, _ := w.StdinPipe()
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Close(); w.Wait() }()
	io.WriteString(in, "PRAGMA wal_autocheckpoint=0; INSERT INTO t VALUES('in wal');\n")
	for start := time.Now(); ; time.Sleep(10 * time.Millisecond) {
		if info, err := os.Stat(db + "-wal"); err == nil && info.Size() > 0 {
			break
		} else if time.Since(start) > 5*time.Second {
			t.Fatal("no WAL")
		}
	}
	write(t, filepath.Join(src, "notes.txt"), "plain")
	c := cfg()
	c.Online = true
	var archive bytes.Buffer
	if err := Export(src, &archive, c, Options{SQLite: tool}); err != nil {
		t.Fatal(err)
	}
	names := entries(t, archive.Bytes())
	sort.Strings(names)
	if want := []string{"data/app.db", "data/app.db-wal", "data/notes.txt", HeaderName}; !reflect.DeepEqual(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	dst := t.TempDir()
	if err := Import(dst, &archive, c, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := query(t, tool, filepath.Join(dst, "app.db"), "SELECT group_concat(v) FROM t;"); got != "kept,in wal" {
		t.Fatalf("restored rows = %q", got)
	}
}

func TestOnlineExportArchivesARollbackJournalDatabase(t *testing.T) {
	tool := sqlite3Tool(t)
	src := t.TempDir()
	query(t, tool, filepath.Join(src, "app.db"), "CREATE TABLE t(v); INSERT INTO t VALUES('kept');")
	c := cfg()
	c.Online = true
	var archive bytes.Buffer
	if err := Export(src, &archive, c, Options{SQLite: tool}); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := Import(dst, &archive, c, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := query(t, tool, filepath.Join(dst, "app.db"), "PRAGMA integrity_check; SELECT v FROM t;"); got != "ok\nkept" {
		t.Fatalf("restored = %q", got)
	}
}

func TestExportLeavesOutExcludedPaths(t *testing.T) {
	src := t.TempDir()
	for _, f := range []string{"keep/exports/a", "keep/b.tmp", "exports/1/files.db", ".backup-app-7/state.db", "c.tmp", "d"} {
		write(t, filepath.Join(src, f), "x")
	}
	c := cfg()
	c.Exclude = []string{"exports", ".backup-app-*", "*.tmp"}
	var archive bytes.Buffer
	if err := Export(src, &archive, c, Options{}); err != nil {
		t.Fatal(err)
	}
	names := entries(t, archive.Bytes())
	sort.Strings(names)
	if want := []string{"data/d", "data/keep/", "data/keep/b.tmp", "data/keep/exports/", "data/keep/exports/a", HeaderName}; !reflect.DeepEqual(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
}

func TestOfflineExportKeepsSQLiteJournalsVerbatim(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "db.sqlite"), "SQLite format 3\x00pages")
	write(t, filepath.Join(src, "db.sqlite-wal"), "wal")
	var archive bytes.Buffer
	if err := Export(src, &archive, cfg(), Options{SQLite: "/nonexistent/sqlite3"}); err != nil {
		t.Fatal(err)
	}
	names := entries(t, archive.Bytes())
	sort.Strings(names)
	if want := []string{"data/db.sqlite", "data/db.sqlite-wal", HeaderName}; !reflect.DeepEqual(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
}

// The walk stats a database before its read transaction begins. A commit and
// a full checkpoint in between grow the file; the export must archive the
// length the transaction sees, or the restore misses the new pages. Before
// the fix the restore was malformed.
func TestOnlineExportSizesTheDatabaseInsideTheReadTransaction(t *testing.T) {
	tool := sqlite3Tool(t)
	src := t.TempDir()
	db := filepath.Join(src, "app.db")
	query(t, tool, db, "PRAGMA journal_mode=WAL; CREATE TABLE t(id INTEGER PRIMARY KEY, b BLOB); INSERT INTO t(b) VALUES(randomblob(100));")
	stale, err := os.Stat(db) // what the walk saw
	if err != nil {
		t.Fatal(err)
	}
	query(t, tool, db, "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x<200) INSERT INTO t(b) SELECT randomblob(4000) FROM c; PRAGMA wal_checkpoint(TRUNCATE);")
	if grown, _ := os.Stat(db); grown.Size() <= stale.Size() {
		t.Fatalf("database did not grow: %d -> %d", stale.Size(), grown.Size())
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	raw, _ := json.Marshal(Header{Format: Format, Package: "demo", DataVersion: "1.2", Online: true})
	tw.WriteHeader(&tar.Header{Name: HeaderName, Mode: 0o600, Size: int64(len(raw)), Typeflag: tar.TypeReg})
	tw.Write(raw)
	if err := exportSQLite(tw, Options{SQLite: tool}, db, "data/app.db", filepath.Join(t.TempDir(), "wal")); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	dst := t.TempDir()
	if err := Import(dst, &archive, cfg(), Options{}); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(tool, filepath.Join(dst, "app.db"), "PRAGMA integrity_check; SELECT count(*) FROM t;").CombinedOutput()
	if got := strings.TrimSpace(string(out)); got != "ok\n201" {
		t.Fatalf("restored database: %q", got)
	}
}

// Each transaction inserts ten rows and rewrites older ones, and checkpoints
// run every few commits, so a restore that mixed two states would show a row
// count that is not a multiple of ten, a gap, or a failed integrity check.
func TestOnlineExportWhileAWriterCommits(t *testing.T) {
	tool := sqlite3Tool(t)
	src := t.TempDir()
	db := filepath.Join(src, "app.db")
	w := exec.Command(tool, "-bail", db)
	var werr bytes.Buffer
	w.Stderr = &werr
	in, _ := w.StdinPipe()
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	io.WriteString(in, ".timeout 5000\nPRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=16; PRAGMA journal_size_limit=0; CREATE TABLE t(id INTEGER PRIMARY KEY, b BLOB);\n")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			fmt.Fprintf(in, "BEGIN; WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x<10) INSERT INTO t(b) SELECT randomblob(3000) FROM c; UPDATE t SET b=randomblob(3000) WHERE id%%7=%d; COMMIT;\n", i%7)
		}
	}()
	defer func() {
		close(stop)
		<-done
		in.Close()
		if err := w.Wait(); err != nil {
			t.Errorf("writer: %v %s", err, werr.String())
		}
	}()
	time.Sleep(300 * time.Millisecond)
	c := cfg()
	c.Online = true
	for i := range 5 {
		var archive bytes.Buffer
		if err := Export(src, &archive, c, Options{SQLite: tool}); err != nil {
			t.Fatalf("export %d: %v", i, err)
		}
		dst := t.TempDir()
		if err := Import(dst, &archive, c, Options{}); err != nil {
			t.Fatal(err)
		}
		got := query(t, tool, filepath.Join(dst, "app.db"), "PRAGMA integrity_check; SELECT count(*), max(id) FROM t;")
		lines := strings.Split(got, "\n")
		var n, max int
		if len(lines) != 2 || lines[0] != "ok" {
			t.Fatalf("export %d: %q", i, got)
		}
		fmt.Sscanf(lines[1], "%d|%d", &n, &max)
		if n == 0 || n%10 != 0 || n != max {
			t.Fatalf("export %d: rows %d, max id %d: not a committed state", i, n, max)
		}
		t.Logf("export %d: %d rows", i, n)
		time.Sleep(400 * time.Millisecond)
	}
}

// APPBACKUP_MEASURE=1 times an online export of a generated 1 GB database
// against the earlier full copy with sqlite3 .backup.
func TestMeasureOnlineExport(t *testing.T) {
	if os.Getenv("APPBACKUP_MEASURE") == "" {
		t.Skip("set APPBACKUP_MEASURE=1")
	}
	tool := sqlite3Tool(t)
	src := t.TempDir()
	db := filepath.Join(src, "app.db")
	query(t, tool, db, "PRAGMA journal_mode=WAL; CREATE TABLE t(id INTEGER PRIMARY KEY, b BLOB); WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x<250000) INSERT INTO t(b) SELECT randomblob(4000) FROM c;")
	info, _ := os.Stat(db)
	c := cfg()
	c.Online = true
	start := time.Now()
	if err := Export(src, io.Discard, c, Options{SQLite: tool}); err != nil {
		t.Fatal(err)
	}
	online := time.Since(start)
	start = time.Now()
	query(t, tool, db, ".backup '"+filepath.Join(t.TempDir(), "copy")+"'")
	t.Logf("database %d MiB: online export to io.Discard %v (no copy); sqlite3 .backup copy alone %v", info.Size()>>20, online, time.Since(start))
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
