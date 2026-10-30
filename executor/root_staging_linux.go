//go:build linux

package main

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const selfFD = "/proc/self/fd/"

func killGroup(pid int) { syscall.Kill(-pid, syscall.SIGKILL) }

func processStart(pid int) (string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	return parseProcessStart(string(b))
}

// stagingDir opens the package staging folder without following a symlink
// below DSM's var link.
func (w subprocessWriter) stagingDir() (*os.File, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(w.Staging))
	if err != nil {
		return nil, err
	}
	return openBeneath(parent, filepath.Base(w.Staging), syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
}

// rootStagingDir opens the staging folder, creating it first when the App has
// not: root output may come before any App code made it. A folder the
// executor creates is the package account's, mode 0700, so the App reads it.
func (w subprocessWriter) rootStagingDir() (*os.File, error) {
	dir, err := w.stagingDir()
	if !errors.Is(err, syscall.ENOENT) {
		if err == nil {
			var st syscall.Stat_t
			if syscall.Fstat(int(dir.Fd()), &st) != nil || st.Uid != uint32(w.PackageUID) || st.Mode&0077 != 0 {
				dir.Close()
				return nil, errors.New("staging must be private and package-owned")
			}
		}
		return dir, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(w.Staging))
	if err != nil {
		return nil, err
	}
	p, err := openBeneath("/", strings.TrimPrefix(parent, "/"), syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	base := filepath.Base(w.Staging)
	created := syscall.Mkdirat(int(p.Fd()), base, 0700)
	if created != nil && !errors.Is(created, syscall.EEXIST) {
		return nil, created
	}
	fd, err := syscall.Openat(int(p.Fd()), base, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir = os.NewFile(uintptr(fd), base)
	if created == nil {
		if err = dir.Chown(w.PackageUID, w.PackageGID); err == nil {
			err = dir.Chmod(0700)
		}
	}
	if err != nil {
		dir.Close()
		return nil, err
	}
	return dir, nil
}

// Create makes root-<random> as root, exclusively and without following a
// symlink, and gives it to the package account with mode 0600.
func (w subprocessWriter) Create() (*os.File, string, error) {
	dir, err := w.rootStagingDir()
	if err != nil {
		return nil, "", err
	}
	defer dir.Close()
	id, err := randomID()
	if err != nil {
		return nil, "", err
	}
	name := "root-" + id[:32]
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDWR|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, "", err
	}
	f := os.NewFile(uintptr(fd), name)
	// Active raw transfers can outlive orphan retention. A scanner must not
	// reclaim an open output just because its last write was over an hour ago.
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		err = f.Chown(w.PackageUID, w.PackageGID)
	}
	if err != nil {
		f.Close()
		syscall.Unlinkat(int(dir.Fd()), name)
		return nil, "", err
	}
	return f, name, nil
}

func (w subprocessWriter) Available() (int64, error) {
	dir, err := w.rootStagingDir()
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	var st syscall.Statfs_t
	if err := syscall.Fstatfs(int(dir.Fd()), &st); err != nil {
		return 0, err
	}
	block := uint64(st.Bsize)
	if block == 0 {
		return 0, os.ErrInvalid
	}
	if st.Bavail > math.MaxInt64/block {
		return math.MaxInt64, nil
	}
	return int64(st.Bavail * block), nil
}

func (w subprocessWriter) Remove(name string) {
	if dir, err := w.stagingDir(); err == nil && validName(name) && strings.HasPrefix(name, "root-") {
		syscall.Unlinkat(int(dir.Fd()), name)
		dir.Close()
	}
}

// Used counts regular root-* files; one older than an hour no client took is
// removed first.
func (w subprocessWriter) Used() (int64, error) {
	dir, err := w.rootStagingDir()
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return 0, err
	}
	var used int64
	for _, name := range names {
		if !strings.HasPrefix(name, "root-") {
			continue
		}
		// Opened without following: a symlink is no output.
		fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		var st syscall.Stat_t
		err = syscall.Fstat(fd, &st)
		locked := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) != nil
		syscall.Close(fd)
		if err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG {
			continue
		}
		if !locked && time.Since(time.Unix(st.Mtim.Unix())) > rootStagingAge {
			syscall.Unlinkat(int(dir.Fd()), name)
			continue
		}
		used += st.Size
	}
	return used, nil
}
