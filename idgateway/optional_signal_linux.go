//go:build linux

package idgateway

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// The kernel also enforces signal ownership. Check the process UID explicitly
// and refuse root so a bad/stale PID cannot cross the package UID boundary.
func notifyOptionalGateway(dir string) error {
	if os.Geteuid() == 0 {
		return errors.New("optional passthrough control must run as the package account")
	}
	path := filepath.Join(dir, "gateway.pid")
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16 {
		return errors.New("invalid gateway pid file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		return errors.New("invalid gateway pid")
	}
	proc, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	if err != nil {
		return err
	}
	st, ok := proc.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return errors.New("gateway process has a different UID")
	}
	return syscall.Kill(pid, syscall.SIGHUP)
}

func (f *Front) watchOptional(ctx context.Context) (func(), error) {
	if f.optional == nil {
		return func() {}, nil
	}
	reload, stop := context.WithCancel(ctx)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	if err := replaceOptionalFile(f.optional.dir, "gateway.pid", []byte(strconv.Itoa(os.Getpid())+"\n")); err != nil {
		signal.Stop(ch)
		stop()
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-reload.Done():
				return
			case <-ch:
				f.ReloadOptionalPassthrough()
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		stop()
		<-done
		f.optional.mu.Lock()
		f.optional.cancel()
		f.optional.mu.Unlock()
		path := filepath.Join(f.optional.dir, "gateway.pid")
		if raw, _ := os.ReadFile(path); strings.TrimSpace(string(raw)) == strconv.Itoa(os.Getpid()) {
			os.Remove(path)
		}
	}, nil
}
