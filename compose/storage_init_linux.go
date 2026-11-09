package compose

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// Walk file descriptors with O_NOFOLLOW, including every base component.
// A concurrent rename cannot redirect initialization through a symlink.
func initializeStorageDirectory(d WritableDirectory) error {
	if os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		return fmt.Errorf("storage initialization requires the nonroot package UID")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { unix.Close(fd) }()
	for _, part := range strings.Split(strings.TrimPrefix(d.Base, "/"), "/") {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		unix.Close(fd)
		fd = next
	}
	var base unix.Stat_t
	if err := unix.Fstat(fd, &base); err != nil {
		return err
	}
	// A Shared Folder root is owned by DSM or an administrator and its ACL often
	// reads as mode 0777; DSM's grant, not the mode bits, decides access there.
	if !d.Share && base.Mode&0022 != 0 && base.Mode&unix.S_ISVTX == 0 {
		return fmt.Errorf("a shared writable base must have the sticky bit")
	}
	for _, part := range strings.Split(d.Path, "/") {
		if err := unix.Mkdirat(fd, part, 0700); err != nil && err != unix.EEXIST {
			return err
		} else if err == nil {
			if err := unix.Fsync(fd); err != nil {
				return err
			}
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		unix.Close(fd)
		fd = next
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			return err
		}
		if !d.Share && (st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0) {
			return fmt.Errorf("existing data must be package-owned with private permissions")
		}
	}
	return probeWritable(fd)
}

func openNoFollow(target string) (int, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(target, "/"), "/") {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
	}
	return fd, nil
}

// resetScratchDirectory empties a package-owned scratch mount and sets it to
// mode 0700, so tools that check their temp parent (owner and no group/other
// write) accept it whatever the host's default permissions were.
func resetScratchDirectory(target string) error {
	if os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		return fmt.Errorf("scratch requires the nonroot package UID")
	}
	fd, err := openNoFollow(target)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("scratch must be owned by the package UID, found UID %d", st.Uid)
	}
	if err := unix.Fchmod(fd, 0700); err != nil {
		return err
	}
	if err := emptyDirectory(fd, 0); err != nil {
		return err
	}
	return probeWritable(fd)
}

// emptyDirectory removes every entry below fd without following symlinks.
// Each pass rereads the directory, so removal never races its own listing.
func emptyDirectory(fd, depth int) error {
	if depth > 128 {
		return fmt.Errorf("scratch tree is too deep to clean")
	}
	for {
		dup, err := unix.Dup(fd)
		if err != nil {
			return err
		}
		if _, err := unix.Seek(dup, 0, io.SeekStart); err != nil {
			unix.Close(dup)
			return err
		}
		dir := os.NewFile(uintptr(dup), "scratch")
		names, err := dir.Readdirnames(-1)
		dir.Close()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return nil
		}
		for _, name := range names {
			if e := unix.Unlinkat(fd, name, 0); e == nil || e == unix.ENOENT {
				continue
			} else if e != unix.EISDIR && e != unix.EPERM {
				return e
			}
			child, e := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if e != nil {
				return e
			}
			// The App may have left a read-only subtree; it is still its own.
			unix.Fchmod(child, 0700)
			e = emptyDirectory(child, depth+1)
			unix.Close(child)
			if e != nil {
				return e
			}
			if e := unix.Unlinkat(fd, name, unix.AT_REMOVEDIR); e != nil && e != unix.ENOENT {
				return e
			}
		}
	}
}

func probeWritable(fd int) error {
	// An actual exclusive write detects EACCES, read-only filesystems, quota and
	// disk-full errors that a mode-bit or access(2) check alone cannot establish.
	name := ".sdk-write-" + rand.Text()
	probe, err := unix.Openat(fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(fd, name, 0)
	_, err = unix.Write(probe, []byte("writable\n"))
	if err == nil {
		err = unix.Fsync(probe)
	}
	if e := unix.Close(probe); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = unix.Unlinkat(fd, name, 0); err != nil {
		return err
	}
	return unix.Fsync(fd)
}
