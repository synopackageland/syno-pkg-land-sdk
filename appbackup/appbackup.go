// Package appbackup exports and imports a package's data directory for
// Hyper Backup application backup.
//
// Hyper Backup runs a package's backup scripts as root. The CLI generates
// those scripts from a fixed template that only switches to the package
// account and moves one archive between Hyper Backup's temporary folder and
// this package. Everything that reads or writes App data happens here, as the
// package account: root never parses the archive.
//
// The archive is a tar stream. Its first entry is HeaderName, which records
// the package and its data version; every other entry is below "data/" and
// mirrors the data directory (the package's `var` folder).
package appbackup

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Schema is the Config schema this package reads.
const Schema = 1

// Format is the archive format this package writes and reads.
const Format = 1

// HeaderName is the first archive entry.
const HeaderName = "synopkgland-backup.json"

// stagingPrefix names the private folders an export or import keeps inside
// the data directory while it runs. They are never archived.
const stagingPrefix = ".synopkgland-backup-"

const hookTimeout = 10 * time.Minute

var (
	// ErrInvalid reports an invalid configuration or an unsafe archive.
	ErrInvalid = errors.New("APP_BACKUP_INVALID")
	// ErrIncompatible reports a backup from another package, another archive
	// format or a newer data version.
	ErrIncompatible = errors.New("APP_BACKUP_INCOMPATIBLE")
)

var packagePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})$`)

// Config is the build-time backup configuration the CLI ships next to the
// control binary.
type Config struct {
	Schema  int    `json:"schema"`
	Package string `json:"package"`
	// DataVersion is "major.minor". A backup restores into the same major
	// version with the same or a newer minor version.
	DataVersion string `json:"dataVersion"`
	// Online exports while the App runs. SQLite databases are then copied
	// through SQLite's backup API instead of byte for byte.
	Online bool `json:"online,omitempty"`
	// Hook is an optional executable, relative to the package target. It runs
	// as `<hook> freeze` before an export and `<hook> thaw` after it.
	Hook string `json:"hook,omitempty"`
}

// Header is the archive's first entry.
type Header struct {
	Format      int    `json:"format"`
	Package     string `json:"package"`
	DataVersion string `json:"dataVersion"`
	Online      bool   `json:"online,omitempty"`
}

// Options are runtime settings that do not come from Config.
type Options struct {
	// Target is the package target folder the hook path is relative to.
	Target string
	// SQLite is the sqlite3 tool used for online snapshots; default
	// /usr/bin/sqlite3.
	SQLite string
	// SnapshotSQLite replaces the sqlite3 tool, for tests.
	SnapshotSQLite func(from, to string) error
}

// Validate checks c.
func (c Config) Validate() error {
	if c.Schema != Schema || !packagePattern.MatchString(c.Package) || !versionPattern.MatchString(c.DataVersion) {
		return ErrInvalid
	}
	if c.Hook != "" && (path.Clean(c.Hook) != c.Hook || !filepath.IsLocal(c.Hook) || strings.Contains(c.Hook, `\`)) {
		return ErrInvalid
	}
	return nil
}

// LoadConfig reads and validates a Config file.
func LoadConfig(file string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(file)
	if err != nil {
		return c, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return c, c.Validate()
}

// Compatible reports whether data written at version backup can be restored
// into an App at version current.
func Compatible(backup, current string) error {
	bm, bn, ok1 := parseVersion(backup)
	cm, cn, ok2 := parseVersion(current)
	if !ok1 || !ok2 || bm != cm || bn > cn {
		return fmt.Errorf("%w: backup data version %q cannot be restored into data version %q", ErrIncompatible, backup, current)
	}
	return nil
}

func parseVersion(v string) (int, int, bool) {
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major, minor, true
}

// Export writes dataDir as an archive to w.
func Export(dataDir string, w io.Writer, c Config, o Options) (err error) {
	if err := c.Validate(); err != nil {
		return err
	}
	// DSM links /var/packages/<package>/var to the volume; walk the folder itself.
	if dataDir, err = filepath.EvalSymlinks(dataDir); err != nil {
		return err
	}
	if err := removeStaging(dataDir); err != nil {
		return err
	}
	if c.Hook != "" {
		if err := runHook(c, o, "freeze"); err != nil {
			return err
		}
		defer func() {
			if thaw := runHook(c, o, "thaw"); err == nil {
				err = thaw
			}
		}()
	}
	var staging string
	if c.Online {
		if staging, err = os.MkdirTemp(dataDir, stagingPrefix+"export-"); err != nil {
			return err
		}
		defer os.RemoveAll(staging)
	}
	tw := tar.NewWriter(w)
	raw, _ := json.Marshal(Header{Format: Format, Package: c.Package, DataVersion: c.DataVersion, Online: c.Online})
	if err := tw.WriteHeader(&tar.Header{Name: HeaderName, Mode: 0o600, Size: int64(len(raw)), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return err
	}
	if _, err := tw.Write(raw); err != nil {
		return err
	}
	snapshots := 0
	err = filepath.WalkDir(dataDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dataDir {
			return err
		}
		rel, _ := filepath.Rel(dataDir, p)
		if !strings.Contains(rel, string(filepath.Separator)) && strings.HasPrefix(rel, stagingPrefix) {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if !mode.IsRegular() && !mode.IsDir() && mode&os.ModeSymlink == 0 {
			return nil // sockets, pipes and devices are runtime state
		}
		source := p
		if c.Online && mode.IsRegular() {
			if journalOfSQLite(p) {
				return nil
			}
			if isSQLite(p) {
				snapshots++
				snap := filepath.Join(staging, strconv.Itoa(snapshots))
				if err := snapshot(o, p, snap); err != nil {
					return fmt.Errorf("snapshot %s: %w", rel, err)
				}
				snapInfo, err := os.Stat(snap)
				if err != nil {
					return err
				}
				info, source = sized{info, snapInfo.Size()}, snap
			}
		}
		link := ""
		if mode&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = "data/" + filepath.ToSlash(rel)
		if mode.IsDir() {
			h.Name += "/"
		}
		h.Uid, h.Gid, h.Uname, h.Gname, h.Format = 0, 0, "", "", tar.FormatPAX
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if !mode.IsRegular() {
			return nil
		}
		f, err := os.Open(source)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.CopyN(tw, f, h.Size)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

// sized reports a snapshot's size with the live file's name, mode and time.
type sized struct {
	os.FileInfo
	size int64
}

func (s sized) Size() int64 { return s.size }

func isSQLite(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	magic := make([]byte, 16)
	_, err = io.ReadFull(f, magic)
	return err == nil && string(magic) == "SQLite format 3\x00"
}

func journalOfSQLite(p string) bool {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if base, ok := strings.CutSuffix(p, suffix); ok && isSQLite(base) {
			return true
		}
	}
	return false
}

func snapshot(o Options, from, to string) error {
	if o.SnapshotSQLite != nil {
		return o.SnapshotSQLite(from, to)
	}
	if strings.ContainsAny(to, `'"`) || !filepath.IsAbs(from) {
		return ErrInvalid
	}
	tool := o.SQLite
	if tool == "" {
		tool = "/usr/bin/sqlite3"
	}
	out, err := exec.Command(tool, "-bail", from, ".backup '"+to+"'").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runHook(c Config, o Options, action string) error {
	if o.Target == "" {
		return fmt.Errorf("%w: hook needs the package target", ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(o.Target, c.Hook), action)
	// Stdout carries the archive, so the hook only writes to stderr.
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("backup hook %s: %w", action, err)
	}
	return nil
}

func removeStaging(dataDir string) error {
	list, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	for _, e := range list {
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			if err := os.RemoveAll(filepath.Join(dataDir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// Import replaces the contents of dataDir with the archive read from r. The
// archive is unpacked into a private folder first, so a refused or broken
// archive leaves the current data unchanged.
func Import(dataDir string, r io.Reader, c Config, o Options) (err error) {
	if err := c.Validate(); err != nil {
		return err
	}
	if dataDir, err = filepath.EvalSymlinks(dataDir); err != nil {
		return err
	}
	if err := removeStaging(dataDir); err != nil {
		return err
	}
	tr := tar.NewReader(r)
	h, err := tr.Next()
	if err != nil || h.Name != HeaderName || h.Typeflag != tar.TypeReg || h.Size > 64<<10 {
		return fmt.Errorf("%w: not an App backup archive", ErrInvalid)
	}
	var header Header
	if err := json.NewDecoder(io.LimitReader(tr, h.Size)).Decode(&header); err != nil {
		return fmt.Errorf("%w: unreadable archive header", ErrInvalid)
	}
	if header.Format != Format || header.Package != c.Package {
		return fmt.Errorf("%w: archive for %q format %d", ErrIncompatible, header.Package, header.Format)
	}
	if err := Compatible(header.DataVersion, c.DataVersion); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(dataDir, stagingPrefix+"import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := unpack(tr, staging); err != nil {
		return err
	}
	return swap(dataDir, staging)
}

func unpack(tr *tar.Reader, staging string) error {
	seen, links := map[string]bool{}, map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		name := h.Name
		if h.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		rel, ok := strings.CutPrefix(name, "data/")
		if !ok || rel == "" || path.Clean(rel) != rel || !filepath.IsLocal(rel) || strings.Contains(rel, `\`) || seen[rel] {
			return fmt.Errorf("%w: unsafe archive entry %q", ErrInvalid, h.Name)
		}
		for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
			if links[parent] {
				return fmt.Errorf("%w: archive entry %q is below a symbolic link", ErrInvalid, h.Name)
			}
		}
		seen[rel] = true
		dest := filepath.Join(staging, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		perm := os.FileMode(h.Mode) & os.ModePerm
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(dest, 0o700); err != nil && !os.IsExist(err) {
				return err
			}
			if err := os.Chmod(dest, perm|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			if err := os.Chmod(dest, perm); err != nil {
				return err
			}
			if err := os.Chtimes(dest, h.ModTime, h.ModTime); err != nil {
				return err
			}
		case tar.TypeSymlink:
			links[rel] = true
			if err := os.Symlink(h.Linkname, dest); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unsupported archive entry %q", ErrInvalid, h.Name)
		}
	}
}

// swap removes the current data and moves the unpacked data into place.
func swap(dataDir, staging string) error {
	current, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	for _, e := range current {
		if p := filepath.Join(dataDir, e.Name()); p != staging {
			if err := os.RemoveAll(p); err != nil {
				return err
			}
		}
	}
	restored, err := os.ReadDir(staging)
	if err != nil {
		return err
	}
	for _, e := range restored {
		if err := os.Rename(filepath.Join(staging, e.Name()), filepath.Join(dataDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
