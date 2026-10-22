//go:build linux

package compose

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func discoverGPU() ([]gpuNode, int) {
	group, err := user.LookupGroup("videodriver")
	if err != nil {
		return nil, 0
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil || gid <= 0 {
		return nil, 0
	}
	dir, err := os.Lstat("/dev/dri")
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 {
		return nil, 0
	}
	paths, err := filepath.Glob("/dev/dri/renderD*")
	if err != nil || len(paths) > 64 {
		return nil, 0
	}
	var nodes []gpuNode
	for _, path := range paths {
		st, err := os.Lstat(path)
		if err != nil {
			continue
		}
		native, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		vendor, err := os.ReadFile(filepath.Join("/sys/class/drm", filepath.Base(path), "device/vendor"))
		if err != nil {
			continue
		}
		nodes = append(nodes, gpuNode{Path: path, Vendor: strings.TrimSpace(string(vendor)), Major: unix.Major(uint64(native.Rdev)), Minor: unix.Minor(uint64(native.Rdev)), GID: native.Gid, Mode: st.Mode(), Character: st.Mode()&os.ModeCharDevice != 0, Symlink: st.Mode()&os.ModeSymlink != 0})
	}
	return nodes, gid
}
