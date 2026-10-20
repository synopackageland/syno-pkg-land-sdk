package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// approvedExecutable reports whether a /proc/<pid>/exe link and the inode it
// runs (stat of that link) belong to an executor root installed in runtime.
// The inode decides, not the name: kernel 3.10 (DSM 7.1) names a
// rename-replaced inode after the rename source, so a monitor whose executor
// was replaced reads as .executor (control.sh) or .executor-* (installAnchored)
// "(deleted)". Only root writes runtime (root:root 0700), and a replaced inode
// must be unlinked on the runtime's device.
func approvedExecutable(link string, running os.FileInfo, runtime string, trusted func(os.FileInfo) bool) bool {
	name, deleted := strings.CutSuffix(link, " (deleted)")
	base := filepath.Base(name)
	if filepath.Dir(name) != runtime || (base != "executor" && !strings.HasPrefix(base, ".executor")) {
		return false
	}
	if !running.Mode().IsRegular() || !trusted(running) {
		return false
	}
	if !deleted {
		current, err := os.Lstat(filepath.Join(runtime, "executor"))
		return err == nil && os.SameFile(running, current)
	}
	dir, err := os.Stat(runtime)
	if err != nil {
		return false
	}
	st, ok := running.Sys().(*syscall.Stat_t)
	dt, dirOK := dir.Sys().(*syscall.Stat_t)
	return ok && dirOK && st.Nlink == 0 && st.Dev == dt.Dev
}
