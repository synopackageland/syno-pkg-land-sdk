//go:build unix

package bridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/background"
)

var geteuid = os.Geteuid

// Exec starts the App once per call and talks over its stdin and stdout
// (mode "exec"). The App gets Env and nothing of the CGI environment, which
// holds the browser's cookie and SynoToken.
type Exec struct {
	Path string
	Args []string
	Dir  string
	Env  []string
}

func (e *Exec) Dial(ctx context.Context) (net.Conn, error) {
	if err := background.RefuseRoot(geteuid()); err != nil {
		return nil, fmt.Errorf("bridge: the App never runs as root: %w", err)
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		return nil, err
	}
	cmd := exec.Command(e.Path, e.Args...)
	cmd.Dir = e.Dir
	cmd.Env = append(append([]string{}, e.Env...), "SYNOPKGLAND_BRIDGE=stdio")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdoutW, os.Stderr
	// Its own process group, so Close also ends what the App started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	stdinR.Close()
	stdoutW.Close()
	if err != nil {
		stdinW.Close()
		stdoutR.Close()
		return nil, err
	}
	return &procConn{in: stdinW, out: stdoutR, cmd: cmd}, nil
}

// procConn is a child process seen as a connection.
type procConn struct {
	in  *os.File
	out *os.File
	cmd *exec.Cmd
}

func (c *procConn) Read(b []byte) (int, error)  { return c.out.Read(b) }
func (c *procConn) Write(b []byte) (int, error) { return c.in.Write(b) }

func (c *procConn) Close() error {
	c.in.Close()
	c.out.Close()
	syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	return c.cmd.Wait()
}

func (c *procConn) LocalAddr() net.Addr  { return pipeAddr{} }
func (c *procConn) RemoteAddr() net.Addr { return pipeAddr{} }
func (c *procConn) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}
func (c *procConn) SetReadDeadline(t time.Time) error  { return c.out.SetReadDeadline(t) }
func (c *procConn) SetWriteDeadline(t time.Time) error { return c.in.SetWriteDeadline(t) }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "app" }
