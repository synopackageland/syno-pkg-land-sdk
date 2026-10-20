//go:build linux

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

type readSpec struct {
	Subject  Subject `json:"subject"`
	Root     string  `json:"root"`
	Relative string  `json:"relative"`
	Limit    int64   `json:"limit"`
}

var errNotFile = errors.New("not a regular file")
var errTooLarge = errors.New("file exceeds the limit")

// Read creates a private staging file as root, hands it to a child that drops
// to the subject and copies the source into it, and names the staged file.
// The kernel checks the subject's own permission to open the source.
func (w subprocessWriter) Read(ctx context.Context, s Subject, root, relative string, limit int64) (string, int64, error) {
	if !cleanPath(root) || !validRelative(relative) || limit <= 0 || limit > maxReadBytes {
		return "", 0, errors.New("request")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(w.Staging))
	if err != nil {
		return "", 0, err
	}
	dir, err := openBeneath(parent, filepath.Base(w.Staging), syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return "", 0, err
	}
	defer dir.Close()
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return "", 0, err
	}
	name := "read-" + hex.EncodeToString(random)
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return "", 0, err
	}
	out := os.NewFile(uintptr(fd), name)
	defer out.Close()
	kept := false
	defer func() {
		if !kept {
			syscall.Unlinkat(int(dir.Fd()), name)
		}
	}()
	if err = out.Chown(w.PackageUID, w.PackageGID); err != nil {
		return "", 0, err
	}
	spec, err := json.Marshal(readSpec{Subject: s, Root: root, Relative: relative, Limit: limit})
	if err != nil {
		return "", 0, err
	}
	// Stdin stays open while the child copies: its EOF is the parent-death signal.
	input, inWrite, err := os.Pipe()
	if err != nil {
		return "", 0, err
	}
	defer input.Close()
	defer inWrite.Close()
	cmd := command(ctx, w.Executable, []string{"read-child"})
	var answer cappedBuffer
	cmd.Stdin = input
	cmd.Stdout = &answer
	cmd.Stderr = io.Discard
	cmd.ExtraFiles = []*os.File{out}
	if err = cmd.Start(); err != nil {
		return "", 0, err
	}
	input.Close()
	_, err = inWrite.Write(append(spec, '\n'))
	waitErr := cmd.Wait()
	if err != nil {
		return "", 0, err
	}
	var ev struct {
		Kind string `json:"kind"`
		Code string `json:"code,omitempty"`
		Size int64  `json:"size,omitempty"`
	}
	if strictJSON(answer.buf.Bytes(), &ev) != nil {
		return "", 0, errors.New("read child")
	}
	if ev.Kind == "refused" && contains([]string{"limit-exceeded", "PERMISSION_DENIED", "not-found", "not-a-file", "invalid-path"}, ev.Code) {
		return "", 0, readRefusedError{ev.Code}
	}
	info, err := out.Stat()
	if waitErr != nil || ev.Kind != "read" || err != nil || info.Size() != ev.Size || ev.Size > limit {
		return "", 0, errors.New("read child")
	}
	kept = true
	return name, ev.Size, nil
}

func runReadChild() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if os.Geteuid() != 0 {
		return errors.New("root child required")
	}
	input := bufio.NewReader(os.Stdin)
	line, err := input.ReadBytes('\n')
	if err != nil || len(line) > maxRequest {
		return errors.New("spec")
	}
	var spec readSpec
	if strictJSON(line, &spec) != nil || !cleanPath(spec.Root) || !validRelative(spec.Relative) || spec.Limit <= 0 || spec.Limit > maxReadBytes {
		return errors.New("spec")
	}
	if err = dropSealedIdentity(realIdentity{}, spec.Subject); err != nil {
		return err
	}
	out := os.NewFile(3, "staging")
	defer out.Close()
	send := func(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }
	n, err := readInto(spec.Root, spec.Relative, out, spec.Limit, awaitControl(input))
	if code := readCode(err); code != "" {
		return send(map[string]any{"kind": "refused", "code": code})
	}
	if err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return send(map[string]any{"kind": "read", "size": n})
}

// readInto copies the regular file root/relative, opened without following
// any symlink, into out. A file that changes size while copied fails.
func readInto(root, relative string, out io.Writer, limit int64, control <-chan controlLine) (int64, error) {
	f, err := openBeneath(root, relative, syscall.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, errNotFile
	}
	if info.Size() > limit {
		return 0, errTooLarge
	}
	return info.Size(), copyUnlessParentGone(out, f, info.Size(), control)
}

// readCode is the SDK code of a refusal the caller can act on, or "".
func readCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errTooLarge):
		return "limit-exceeded"
	case errors.Is(err, errNotFile):
		return "not-a-file"
	case errors.Is(err, syscall.ENOENT):
		return "not-found"
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return "PERMISSION_DENIED"
	case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.ENOTDIR):
		return "invalid-path"
	}
	return ""
}
