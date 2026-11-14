package selfrestart

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"syscall"
	"time"
)

type record struct {
	Operation
	Owner        string `json:"owner"`
	PermitHash   string `json:"permit_hash,omitempty"`
	Acknowledged bool   `json:"acknowledged,omitempty"`
}
type journal struct {
	Records map[string]record `json:"records"`
}

// One private journal and a stable lock inode serialize independent CGI
// processes. fsync/rename/directory-fsync precede disclosure of any permit.
func (s *Service) journal(ctx context.Context, change func(*journal) error) error {
	if err := os.MkdirAll(s.config.StateDir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(s.config.StateDir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return ErrRefused
	}
	root, err := os.OpenRoot(s.config.StateDir)
	if err != nil {
		return err
	}
	defer root.Close()
	anchored, err := root.Stat(".")
	if err != nil || !os.SameFile(info, anchored) {
		return ErrRefused
	}
	lock, err := root.OpenFile("lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if !privateFile(lock) {
		return ErrRefused
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	j := journal{Records: map[string]record{}}
	var before []byte
	f, err := root.OpenFile("operations.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err == nil {
		if !privateFile(f) {
			f.Close()
			return ErrRefused
		}
		before, err = io.ReadAll(io.LimitReader(f, 1<<20+1))
		f.Close()
		if err != nil || len(before) > 1<<20 || json.Unmarshal(before, &j) != nil || j.Records == nil || len(j.Records) > 100 {
			return ErrRefused
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = change(&j); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	after, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if string(before) == string(after) {
		return nil
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	name := "write-" + id
	out, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	_, err = out.Write(after)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = root.Rename(name, "operations.json"); err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func privateFile(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && info.Sys().(*syscall.Stat_t).Uid == uint32(os.Geteuid()) && info.Sys().(*syscall.Stat_t).Nlink == 1
}
