package main

import (
	"errors"
	"os"
	"syscall"
)

func directoryMatches(info os.FileInfo, mode os.FileMode, uid, gid int) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && info.Mode().Perm() == mode && int(st.Uid) == uid && int(st.Gid) == gid
}

// Only repair permissions on a directory this invocation created. mkdir's
// requested mode is reduced by the inherited boot-task umask (normally 0077).
func createDirectory(path string, mode os.FileMode, uid, gid int) (os.FileInfo, error) {
	if err := os.Mkdir(path, mode); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if err = f.Chown(uid, gid); err != nil {
		return nil, err
	}
	if err = f.Chmod(mode); err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !directoryMatches(info, mode, uid, gid) {
		return nil, errors.New("created directory permissions")
	}
	return info, nil
}
