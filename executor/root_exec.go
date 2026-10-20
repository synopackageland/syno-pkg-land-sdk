package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// rootBaseEnv is every root program's environment besides the executor's
// SYNOPKGLAND_* variables; nothing is inherited.
var rootBaseEnv = []string{"PATH=/usr/syno/bin:/usr/syno/sbin:/usr/bin:/bin", "LANG=C", "HOME=/"}

// rootPrograms runs the pinned programs of runtime/root. It opens each once,
// checks it is root's, not group or world writable, and has the pinned
// digest, then executes that same inode from the descriptor.
type rootPrograms struct {
	Dir        string // runtime
	Executable string // the executor, whose root-exec mode execs the program
}

func (r rootPrograms) path(name, kind string) string {
	if kind == "binary" {
		return filepath.Join(r.Dir, "root", name)
	}
	return filepath.Join(r.Dir, "root", name+".sh")
}

// openPinned opens the program and checks it against the pin; the returned
// descriptor is at offset 0.
func openPinned(path, kind, digest string) (*os.File, error) {
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || info.Mode().Perm()&0022 != 0 || !ownedByRoot(info) || (kind == "binary" && info.Mode().Perm()&0100 == 0) {
		f.Close()
		return nil, errors.New("root program ownership or mode")
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil || hex.EncodeToString(h.Sum(nil)) != digest {
		f.Close()
		return nil, errors.New("root program digest")
	}
	if _, err = f.Seek(0, 0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (r rootPrograms) Start(c rootCall) (rootProcess, error) {
	if !tokenRE.MatchString(c.Name) || !c.Entry.valid() || len(c.Files) > 2 {
		return nil, errors.New("root call")
	}
	f, err := openPinned(r.path(c.Name, c.Entry.Kind), c.Entry.Kind, c.Entry.Digest)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	nice := 0
	if c.Mode == "job" && c.Entry.Job != nil {
		nice = c.Entry.Job.nice()
	}
	cmd := exec.Command(r.Executable, append([]string{"root-exec", c.Entry.Kind, strconv.Itoa(nice), c.Name}, c.Args...)...)
	cmd.Env = append(append([]string{}, rootBaseEnv...), c.Env...)
	cmd.Dir = "/"
	cmd.Stdin = bytes.NewReader(c.Stdin)
	cmd.Stdout, cmd.Stderr = c.Stdout, c.Stderr
	cmd.ExtraFiles = append([]*os.File{f}, c.Files...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A grandchild that keeps the output pipes open cannot hold Wait.
	cmd.WaitDelay = 5 * time.Second
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	return &rootCmd{cmd}, nil
}

type rootCmd struct{ cmd *exec.Cmd }

func (p *rootCmd) Wait() (int, error) {
	err := p.cmd.Wait()
	var exit *exec.ExitError
	if err == nil || (errors.As(err, &exit) && exit.ExitCode() >= 0) {
		return p.cmd.ProcessState.ExitCode(), nil
	}
	return -1, err
}

func (p *rootCmd) Kill()      { killGroup(p.cmd.Process.Pid) }
func (p *rootCmd) Group() int { return p.cmd.Process.Pid }

// runRootExec is root-exec <kind> <nice> <name> [args...]: the child of
// rootPrograms. With the pinned program on fd 3 it lowers its priority, turns
// off core dumps and replaces itself with the program, which keeps the
// process group, environment and descriptors.
func runRootExec(args []string) error {
	if os.Geteuid() != 0 || len(args) < 3 || (args[0] != "script" && args[0] != "binary") || !tokenRE.MatchString(args[2]) {
		return errors.New("root-exec arguments")
	}
	nice, err := strconv.Atoi(args[1])
	if err != nil || nice < 0 || nice > 19 {
		return errors.New("root-exec nice")
	}
	if err = syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}); err != nil {
		return err
	}
	if err = syscall.Setpriority(syscall.PRIO_PROCESS, 0, nice); err != nil {
		return err
	}
	program := selfFD + "3"
	if args[0] == "script" {
		return syscall.Exec("/bin/sh", append([]string{"sh", program}, args[3:]...), os.Environ())
	}
	return syscall.Exec(program, append([]string{args[2]}, args[3:]...), os.Environ())
}
