package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/budget"
	"io"
	"os"
	"os/exec"
	"time"
)

const maxOutput = 1 << 20

type cappedBuffer struct {
	buf bytes.Buffer
	n   int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if len(p) > maxOutput-b.n {
		return 0, errors.New("output limit")
	}
	n, err := b.buf.Write(p)
	b.n += n
	return n, err
}

type processCommand struct{}

// StreamBody isolates a DSM streaming handler's outfile from synowebapi's JSON
// control reply. The only output path is the inherited pipe, never an App path.
func (processCommand) StreamBody(ctx context.Context, path string, args []string, body io.Writer) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout(ctx))
	defer cancel()
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	defer w.Close()
	args = append(append([]string{}, args...), "outfile=/proc/self/fd/3")
	cmd := command(ctx, path, args)
	cmd.ExtraFiles = []*os.File{w}
	terminate := func() {
		cancel()
		// Wait may already have reaped the parent while a child holds FD 3.
		// Kill the group explicitly even after CommandContext stops watching.
		if cmd.Cancel != nil {
			_ = cmd.Cancel()
		}
	}
	control := &rootOutput{limit: maxWebAPIReply, spillAt: maxWebAPIReply + 1, failed: make(chan struct{})}
	cmd.Stdout = &cancelWriter{writer: control, cancel: terminate}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	w.Close()
	done := make(chan error, 1)
	go func() { _, copyErr := io.Copy(&cancelWriter{writer: body, cancel: terminate}, r); done <- copyErr }()
	err = cmd.Wait()
	var copyErr error
	select {
	case copyErr = <-done:
	case <-ctx.Done():
		terminate()
		r.Close()
		copyErr = <-done
	}
	if copyErr != nil {
		return nil, copyErr
	}
	if control.err != nil {
		return nil, control.err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return control.mem.Bytes(), err
}

// Stream cancels the process group as soon as the bounded consumer refuses a
// write. Stdout never passes through the generic command's 1 MiB buffer.
func (processCommand) Stream(ctx context.Context, path string, args []string, stdout io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout(ctx))
	defer cancel()
	cmd := command(ctx, path, args)
	output := &cancelWriter{writer: stdout, cancel: cancel}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	err := cmd.Run()
	if output.err != nil {
		return output.err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

type cancelWriter struct {
	writer io.Writer
	cancel context.CancelFunc
	err    error
}

func (w *cancelWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
		w.cancel()
	}
	return n, err
}

func command(ctx context.Context, path string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/syno/bin:/usr/syno/sbin:/usr/bin:/bin", "LANG=C", "HOME=/"}
	cmd.Dir = "/"
	cmd.WaitDelay = time.Second
	configureProcess(cmd)
	return cmd
}

// A DSM command gets defaultCommandTimeout unless its caller set another
// bound with withCommandTimeout, which never exceeds maxCommandTimeout.
const (
	defaultCommandTimeout = 20 * time.Second
	maxCommandTimeout     = 3 * time.Minute
)

type commandTimeoutKey struct{}

func withCommandTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, commandTimeoutKey{}, d)
}

// commandTimeout bounds one DSM command.
func commandTimeout(ctx context.Context) time.Duration {
	d, _ := ctx.Value(commandTimeoutKey{}).(time.Duration)
	if until, ok := ctx.Deadline(); ok {
		remaining := budget.Inner(time.Until(until))
		if d > 0 {
			return min(d, remaining, maxCommandTimeout)
		}
		return min(remaining, maxCommandTimeout)
	}
	if d <= 0 {
		return defaultCommandTimeout
	}
	return min(d, maxCommandTimeout)
}

func (processCommand) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout(ctx))
	defer cancel()
	cmd := command(ctx, path, args)
	var out cappedBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return out.buf.Bytes(), nil
}

const maxExecutorBytes int64 = 128 << 20
const maxPolicyBytes int64 = 1 << 20

func digestFile(ctx context.Context, path string, limit int64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return "", errors.New("digest file limit")
	}
	f, err := openRegular(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return "", errors.New("digest file limit")
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		total += int64(n)
		if total > limit {
			return "", errors.New("digest file limit")
		}
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
