package compose

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func openRuntimeDirectory(dir string) (int, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(dir, "/"), "/") {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return -1, e
		}
		fd = next
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
		unix.Close(fd)
		return -1, fmt.Errorf("runtime directory must remain private and package-owned")
	}
	return fd, nil
}

func runtimeExists(fd int, name string) (bool, error) {
	var st unix.Stat_t
	err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == unix.ENOENT {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 || st.Nlink != 1 {
		return false, fmt.Errorf("existing runtime file must be a private regular file")
	}
	return true, nil
}

func writeRuntimeFile(dirs []WritableDirectory, f RuntimeFile) error {
	dir, name, _ := privateTarget(dirs, f.Target)
	fd, err := openRuntimeDirectory(dir)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	exists, err := runtimeExists(fd, name)
	if err != nil {
		return err
	}
	if exists && f.IfAbsent {
		return nil
	}
	content := []byte(f.Content)
	if f.Source != "" {
		// Nonblocking avoids hanging on a substituted FIFO; only regular image
		// assets/private config files are accepted, never devices or sockets.
		src, e := unix.Open(f.Source, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if e != nil {
			return fmt.Errorf("source unavailable")
		}
		file := os.NewFile(uintptr(src), "runtime-source")
		st, e := file.Stat()
		if e != nil || !st.Mode().IsRegular() || st.Size() > 256<<10 {
			file.Close()
			return fmt.Errorf("source must be a bounded regular file")
		}
		content, e = io.ReadAll(io.LimitReader(file, 256<<10+1))
		file.Close()
		if e != nil || len(content) > 256<<10 {
			return fmt.Errorf("source read failed")
		}
	}
	for _, r := range f.Replace {
		value := string(content)
		count := strings.Count(value, r.Old)
		if count == 0 {
			if r.Optional {
				continue
			}
			return fmt.Errorf("required replacement did not match source")
		}
		// Reject growth before ReplaceAll allocates it. Division avoids integer
		// overflow on 32-bit consumers and prevents a tiny declaration from
		// expanding a bounded image asset into gigabytes of container memory.
		if growth := len(r.New) - len(r.Old); growth > 0 && count > ((256<<10)-len(content))/growth {
			return fmt.Errorf("runtime file is too large")
		}
		content = []byte(strings.ReplaceAll(value, r.Old, r.New))
		if len(content) > 256<<10 {
			return fmt.Errorf("runtime file is too large")
		}
	}
	tmp := ".sdk-config-" + rand.Text()
	out, err := unix.Openat(fd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(fd, tmp, 0)
	file := os.NewFile(uintptr(out), "runtime-config")
	_, err = file.Write(content)
	if err == nil {
		err = file.Sync()
	}
	if e := file.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	// Revalidate after preparation. Rename replaces the directory entry rather
	// than writing through a symlink/hardlink supplied by another process.
	if _, err = runtimeExists(fd, name); err != nil {
		return err
	}
	if err = unix.Renameat(fd, tmp, fd, name); err != nil {
		return err
	}
	return unix.Fsync(fd)
}

func runPrepare(ctx context.Context, dirs []WritableDirectory, c PrepareCommand) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("startup canceled")
	}
	if c.UnlessExists != "" {
		dir, name, _ := privateTarget(dirs, c.UnlessExists)
		fd, err := openRuntimeDirectory(dir)
		if err != nil {
			return err
		}
		exists, err := runtimeExists(fd, name)
		unix.Close(fd)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
	}
	cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	// Upstream output may contain generated credentials. Do not relay it into
	// lifecycle logs or include argv/environment in errors.
	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("startup canceled or deadline exceeded")
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return fmt.Errorf("upstream command could not start")
		}
		code = exit.ExitCode()
	}
	codes := c.SuccessCodes
	if len(codes) == 0 {
		codes = []int{0}
	}
	if !slices.Contains(codes, code) {
		return fmt.Errorf("upstream command exited %d", code)
	}
	return nil
}
