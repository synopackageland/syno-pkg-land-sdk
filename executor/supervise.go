package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// serve exits with this status when another monitor holds executor.pid; the
// flock, not the supervisor, decides which monitor survives.
const exitAlreadyRunning = 3

var errAlreadyRunning = errors.New("already running")

func exitStatus(err error) int {
	if errors.Is(err, errAlreadyRunning) {
		return exitAlreadyRunning
	}
	return 1
}

const (
	minRestartDelay = time.Second
	maxRestartDelay = time.Minute
	stableServeRun  = time.Minute
)

// supervise reruns the approved serve after an abnormal end. A clean exit
// (root replacement, shutdown or purge), another live monitor, termination of
// the supervisor or a purged runtime all end supervision instead.
func supervise(ctx context.Context, run func(ctx context.Context, revived bool) error, runtimePresent func() bool, sleep func(context.Context, time.Duration) bool, now func() time.Time, log func(string)) error {
	delay := minRestartDelay
	revived := false
	for {
		started := now()
		err := run(ctx, revived)
		if err == nil || ctx.Err() != nil {
			return nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == exitAlreadyRunning {
			log("executor supervisor: another monitor is running")
			return nil
		}
		if now().Sub(started) >= stableServeRun {
			delay = minRestartDelay
		}
		if !runtimePresent() {
			log("executor supervisor: runtime removed")
			return nil
		}
		log(fmt.Sprintf("executor supervisor: serve ended (%v); restarting in %s", err, delay))
		if !sleep(ctx, delay) || !runtimePresent() {
			return nil
		}
		delay = min(2*delay, maxRestartDelay)
		revived = true
	}
}

func serveArgs(pkg string, revived bool) []string {
	args := []string{"serve", "--package", pkg}
	if revived {
		args = append(args, "--revived")
	}
	return args
}

// Root control start launches the supervisor in its own session; serve runs as
// its child and still owns executor.pid for root stop/purge/replacement.
func supervisorCommand(exe, pkg string) *exec.Cmd {
	cmd := exec.Command(exe, "supervise", "--package", pkg)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

func runSupervisor(exe, pkg string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	run := func(ctx context.Context, revived bool) error {
		cmd := exec.CommandContext(ctx, exe, serveArgs(pkg, revived)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
		cmd.Dir = "/"
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		return cmd.Run()
	}
	present := func() bool { _, err := os.Lstat(exe); return err == nil }
	sleep := func(ctx context.Context, d time.Duration) bool {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		}
	}
	return supervise(ctx, run, present, sleep, time.Now, func(s string) { log.Print(s) })
}
