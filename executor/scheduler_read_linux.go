package main

import (
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const schedulerDirectory = "/usr/syno/etc/esynoscheduler"
const schedulerDatabase = "esynoscheduler.db"

// This is a short-lived child of the protected executor, in the same bounded
// process group. Its private mount namespace cannot change DSM's mounts or
// daemon state. The native API still checks the current scheduler definitions;
// only its incidental initialization writes land in the tmpfs backup.
func runSchedulerRead(args []string) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || !validSchedulerRead(args) {
		return errors.New("invalid scheduler read")
	}
	runtime.LockOSThread()
	// Never return this thread to the pool with its private filesystem view.
	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		return err
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return err
	}
	var st unix.Statfs_t
	if err := unix.Statfs("/run", &st); err != nil {
		return err
	}
	if st.Type != unix.TMPFS_MAGIC {
		return errors.New("scheduler backup requires tmpfs")
	}
	work, err := os.MkdirTemp("/run", "synopkgland-scheduler-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := unix.Mount("tmpfs", work, "tmpfs", unix.MS_NODEV|unix.MS_NOSUID|unix.MS_NOEXEC, "mode=0700,size=67108864"); err != nil {
		return err
	}
	defer unix.Unmount(work, unix.MNT_DETACH)
	for _, name := range []string{"esynoscheduler-conf.json", "esynoscheduler-share-check-utils.sh"} {
		if err := copySchedulerFile(filepath.Join(schedulerDirectory, name), filepath.Join(work, name), 1<<20); err != nil {
			return err
		}
	}
	source := filepath.Join(schedulerDirectory, schedulerDatabase)
	f, err := openRegular(source)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	f.Close()
	if err != nil {
		return err
	}
	if !ownedByRoot(info) || info.Mode().Perm()&0022 != 0 || info.Size() > 32<<20 {
		return errors.New("untrusted scheduler database")
	}
	// SQLite's read-only backup takes a consistent live snapshot, including any
	// committed journal/WAL state. Copying database bytes alone would not do so.
	backup := exec.Command("/usr/bin/sqlite3", "-batch", "-bail", "-readonly", source)
	backup.Env = []string{"PATH=/usr/bin:/bin", "HOME=/", "LANG=C"}
	backup.Stdin = strings.NewReader(".timeout 5000\n.backup " + filepath.Join(work, schedulerDatabase) + "\n")
	backup.Stdout, backup.Stderr = io.Discard, io.Discard
	if err := backup.Run(); err != nil {
		return err
	}
	if err := unix.Mount(work, schedulerDirectory, "", unix.MS_BIND, ""); err != nil {
		return err
	}
	defer unix.Unmount(schedulerDirectory, unix.MNT_DETACH)
	cmd := exec.Command("/usr/syno/bin/synowebapi", args...)
	cmd.Env = []string{"PATH=/usr/syno/bin:/usr/syno/sbin:/usr/bin:/bin", "HOME=/", "LANG=C"}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	// Keep the child's group: the parent's cancellation kills this entire tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return cmd.Run()
}

func copySchedulerFile(source, dest string, limit int64) error {
	f, err := openRegular(source)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !ownedByRoot(info) || info.Mode().Perm()&0022 != 0 || info.Size() > limit {
		return errors.New("untrusted scheduler configuration")
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(f, limit+1))
	if err == nil && n > limit {
		err = errors.New("scheduler configuration grew beyond its limit")
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
