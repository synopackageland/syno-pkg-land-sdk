//go:build linux

package sqlitefile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	fsCompress          = 0x00000004 // FS_COMPR_FL
	fsNoCOW             = 0x00800000 // FS_NOCOW_FL
	compressionProperty = "btrfs.compression"
)

// A nil file with no error means the opened path is on another filesystem.
func openBtrfs(path string) (*os.File, int, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	var stat unix.Statfs_t
	if err := unix.Fstatfs(fd, &stat); err != nil {
		f.Close()
		return nil, 0, err
	}
	if uint32(stat.Type) != unix.BTRFS_SUPER_MAGIC {
		f.Close()
		return nil, 0, nil
	}
	flags, err := unix.IoctlGetInt(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, flags, nil
}

func compressionEnabled(fd int) (bool, error) {
	var buf [16]byte
	n, err := unix.Fgetxattr(fd, compressionProperty, buf[:])
	if errors.Is(err, unix.ENODATA) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch string(buf[:n]) {
	case "", "none", "no":
		return false, nil
	default:
		return true, nil
	}
}

func prepareFlags(flags int, compressed func() (bool, error), setFlags func(int) error) error {
	if flags&(fsNoCOW|fsCompress) != 0 {
		return nil
	}
	hasCompression, err := compressed()
	if err != nil || hasCompression {
		return err
	}
	return setFlags(flags | fsNoCOW)
}

func prepareNoCOW(dir string) error {
	f, flags, err := openBtrfs(dir)
	if f == nil {
		return err
	}
	defer f.Close()
	fd := int(f.Fd())
	return prepareFlags(flags, func() (bool, error) {
		return compressionEnabled(fd)
	}, func(flags int) error {
		return unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, flags)
	})
}

func probeState(path string) (NOCOWState, error) {
	f, flags, err := openBtrfs(path)
	if f == nil {
		return StateUnsupported, err
	}
	f.Close()
	if flags&fsNoCOW != 0 {
		return StateOn, nil
	}
	return StateOff, nil
}

func eligibleDir(dir string) (bool, error) {
	f, flags, err := openBtrfs(dir)
	if f == nil {
		return false, err
	}
	defer f.Close()
	if flags&fsNoCOW == 0 || flags&fsCompress != 0 {
		return false, nil
	}
	compressed, err := compressionEnabled(int(f.Fd()))
	return !compressed && err == nil, err
}

func availableBytes(dir string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

func hasSingleLink(info os.FileInfo) bool {
	return info.Sys().(*syscall.Stat_t).Nlink == 1
}

func checkReplacementMetadata(source, result *os.File, before, after os.FileInfo) error {
	oldStat, newStat := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	if oldStat.Uid != newStat.Uid || oldStat.Gid != newStat.Gid {
		return errors.New("replacement would change database owner or group")
	}
	oldAttrs, err := fileAttributes(int(source.Fd()))
	if err != nil {
		return err
	}
	newAttrs, err := fileAttributes(int(result.Fd()))
	if err != nil {
		return err
	}
	if len(oldAttrs) != len(newAttrs) {
		return errors.New("replacement would change database extended attributes")
	}
	for name, oldValue := range oldAttrs {
		if newValue, found := newAttrs[name]; !found || !bytes.Equal(oldValue, newValue) {
			return fmt.Errorf("replacement would change database extended attribute %q", name)
		}
	}
	return nil
}

func fileAttributes(fd int) (map[string][]byte, error) {
	// Linux limits the total xattr name list and each value to 64 KiB.
	var buf [65536]byte
	n, err := unix.Flistxattr(fd, buf[:])
	if err != nil {
		return nil, err
	}
	names := strings.Split(string(buf[:n]), "\x00")
	attrs := make(map[string][]byte)
	for _, name := range names {
		if name == "" || name == compressionProperty {
			continue
		}
		n, err := unix.Fgetxattr(fd, name, buf[:])
		if err != nil {
			return nil, err
		}
		attrs[name] = bytes.Clone(buf[:n])
	}
	return attrs, nil
}
