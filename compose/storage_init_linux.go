package compose

import (
	"crypto/rand"
	"fmt"
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
	if base.Mode&0022 != 0 && base.Mode&unix.S_ISVTX == 0 {
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
		if st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
			return fmt.Errorf("existing data must be package-owned with private permissions")
		}
	}
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
