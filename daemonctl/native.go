package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
)

type dependencyPolicy struct {
	DNS     []string `json:"dns,omitempty"`
	TCP     []string `json:"tcp,omitempty"`
	Network bool     `json:"network,omitempty"`
}

func (c *controller) dependencies(ctx context.Context) error {
	d := c.Dependencies
	if d == nil {
		return nil
	}
	if d.Network {
		if err := supervision.NetworkReady(ctx, d.DNS...); err != nil {
			return err
		}
	}
	for _, host := range d.DNS {
		if _, err := net.DefaultResolver.LookupIPAddr(ctx, host); err != nil {
			return err
		}
	}
	for _, addr := range d.TCP {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		conn.Close()
	}
	return nil
}

// superviseNative keeps the existing control and stop-fence ownership, while
// every retry decision comes from supervision.Policy. A failed series exits:
// its persisted snapshot survives, but no watcher or timer survives it.
func (c *controller) superviseNative(ready *os.File, signals <-chan os.Signal) error {
	clean := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := record{Supervisor: os.Getpid(), Digest: c.digest, State: "starting", Log: "daemon/daemon.log"}
	if err := c.writeRecord(r); err != nil {
		return err
	}
	socket := filepath.Join(c.dir, "ctl.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return err
	}
	ln.SetUnlinkOnClose(false)
	defer ln.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	conns := make(chan *net.UnixConn)
	go func() {
		for {
			conn, err := ln.AcceptUnix()
			if err != nil {
				return
			}
			select {
			case conns <- conn:
			case <-ctx.Done():
				conn.Close()
				return
			}
		}
	}()
	acked := false
	ack := func() {
		if !acked {
			_, _ = ready.WriteString("ready\n")
			ready.Close()
			acked = true
		}
	}
	cfg := supervision.Defaults()
	if c.Restart != nil {
		cfg.Initial = time.Duration(c.Restart.InitialSeconds) * time.Second
		cfg.Max = time.Duration(c.Restart.MaxSeconds) * time.Second
		cfg.Burst = c.Restart.MaxAttempts
		cfg.Window = time.Duration(c.Restart.WindowSeconds) * time.Second
	}
	p := supervision.New(cfg, nil)
	var tempDir string
	var child *supervision.Process
	var done <-chan struct{}
	var lease func()
	release := func() {
		if lease != nil {
			lease()
			lease = nil
		}
	}
	defer release()
	defer func() {
		if child != nil {
			err := child.Stop(time.Duration(c.Timeouts.Stop) * time.Second / 2)
			if err == nil && tempDir != "" {
				_ = os.RemoveAll(tempDir)
			}
			if err != nil {
				r.State = "failed"
				r.Reason = "DAEMON_CLEANUP_REQUIRED"
				r.RetryAt = time.Time{}
				r.LastExit = &supervision.Exit{Code: -1, Reason: "cleanup_required", Permanent: true}
				_ = c.writeRecord(r)
			} else if clean {
				_ = os.Remove(filepath.Join(c.dir, "daemon.pid"))
			}
		} else if clean {
			_ = os.Remove(filepath.Join(c.dir, "daemon.pid"))
		}
	}()
	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	persist := func(s supervision.Snapshot) error {
		previous := r.State
		r.State = string(s.State)
		r.Attempts = s.Attempts
		r.LastExit = s.LastExit
		r.RetryAt = s.NextRetry
		r.Reason = ""
		switch s.State {
		case supervision.Waiting:
			r.Reason = "DAEMON_WAITING_DEPENDENCY"
		case supervision.Backoff:
			r.Reason = "DAEMON_RESTART_BACKOFF"
		case supervision.Starting:
			r.Reason = "DAEMON_RESTART_STARTING"
		case supervision.Failed:
			r.Reason = "DAEMON_RESTART_LIMIT"
			if s.Reason != "restart_limit" {
				r.Reason = "DAEMON_PERMANENT_ERROR"
			}
		case supervision.Stopped:
			r.State = "exited"
			r.Reason = "DAEMON_CHILD_EXIT"
		}
		if r.State != previous {
			supervisorLog(os.Stderr, "state=%s attempts=%d next=%s reason=%s log=%s", r.State, r.Attempts, r.RetryAt.UTC().Format(time.RFC3339), r.Reason, r.Log)
		}
		return c.writeRecord(r)
	}
	type launchResult struct {
		p       *supervision.Process
		release func()
		err     error
		temp    string
	}
	launched := make(chan launchResult, 1)
	waiting := make(chan struct{}, 1)
	launching := false
	launch := func() error {
		p.Started(time.Now())
		if err := persist(p.Snapshot()); err != nil {
			return err
		}
		launching = true
		go func() {
			_, statErr := os.Stat(c.executable())
			err := statErr
			if err == nil {
				var announce func()
				if c.Dependencies != nil {
					announce = func() {
						select {
						case waiting <- struct{}{}:
						case <-ctx.Done():
						}
					}
				}
				err = supervision.Dependency(ctx, c.dependencies, announce)
			}
			var lease func()
			var proc *supervision.Process
			var temp string
			if err == nil {
				lease, err = supervision.AcquireStart(ctx, func() {
					select {
					case waiting <- struct{}{}:
					case <-ctx.Done():
					}
				})
			}
			if err == nil {
				var lock *os.File
				lock, err = c.lock("launch.lock", time.Now().Add(time.Duration(c.Timeouts.Stop)*time.Second))
				if err == nil {
					if _, gateErr := os.Lstat(filepath.Join(c.dir, "stop.request")); !os.IsNotExist(gateErr) {
						err = context.Canceled
					} else if ctx.Err() != nil {
						err = ctx.Err()
					} else {
						temp, err = os.MkdirTemp(filepath.Join(c.dir, "tmp"), "run-")
						if err == nil {
							cmd := c.child(c.Args)
							cmd.Env = append(cmd.Env, "TMPDIR="+temp)
							cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
							proc, err = supervision.Start(cmd)
						}
					}
					lock.Close()
				}
			}
			launched <- launchResult{proc, lease, err, temp}
		}()
		return nil
	}
	// Join an in-flight launch before releasing the ownership lock. Transfer
	// its child into the normal cleanup defer so even a failed stop retains
	// witnessed inventory and cannot permit a second instance.
	defer func() {
		cancel()
		if launching {
			result := <-launched
			lease = result.release
			if result.p != nil {
				child = result.p
				tempDir = result.temp
				r.Child = child.PID()
				r.Birth = processBirth(r.Child)
				_ = c.writeRecord(r)
			} else if result.temp != "" {
				_ = os.RemoveAll(result.temp)
			}
		}
	}()
	failed := func(exit supervision.Exit) (bool, error) {
		release()
		if tempDir != "" {
			if err := os.RemoveAll(tempDir); err != nil {
				exit.Permanent = true
				exit.Reason = "cleanup_required"
			}
			tempDir = ""
		}
		done = nil
		child = nil
		if c.Restart != nil && ((c.Restart.Mode == "off" && exit.Code != 0) || slices.Contains(c.Restart.FatalExitCodes, exit.Code)) {
			exit.Permanent = true
		}
		s := p.Exited(time.Now(), exit)
		if err := persist(s); err != nil {
			return false, err
		}
		if s.State != supervision.Backoff {
			return false, errors.New(r.Reason)
		}
		r.Restarts++
		if err := c.writeRecord(r); err != nil {
			return false, err
		}
		ack()
		timer = time.NewTimer(time.Until(s.NextRetry))
		timerC = timer.C
		return true, nil
	}
	if err := launch(); err != nil {
		return err
	}
	for {
		select {
		case <-signals:
			clean = true
			cancel()
			return nil
		default:
		}
		select {
		case <-signals:
			clean = true
			cancel()
			return nil
		case <-waiting:
			p.Waiting("dependency")
			if err := persist(p.Snapshot()); err != nil {
				return err
			}
			ack()
		case res := <-launched:
			launching = false
			lease = res.release
			tempDir = res.temp
			if res.err != nil {
				if errors.Is(res.err, context.Canceled) {
					clean = true
					cancel()
					return nil
				}
				if again, err := failed(supervision.ExitOf(res.err)); !again {
					return err
				}
				continue
			}
			child = res.p
			done = child.Done()
			r.Child = child.PID()
			r.Birth = processBirth(r.Child)
			p.Started(time.Now())
			if err := persist(p.Snapshot()); err != nil {
				return err
			}
			timer = time.NewTimer(time.Second)
			timerC = timer.C
		case <-timerC:
			timerC = nil
			if child == nil {
				if err := launch(); err != nil {
					return err
				}
				continue
			}
			if !child.Alive() {
				if again, err := failed(child.Exit()); !again {
					return err
				}
				continue
			}
			release()
			p.Running()
			if err := persist(p.Snapshot()); err != nil {
				return err
			}
			ack()
		case <-done:
			if timer != nil {
				timer.Stop()
				timerC = nil
			}
			if again, err := failed(child.Exit()); !again {
				return err
			}
		case conn := <-conns:
			conn.SetDeadline(time.Now().Add(time.Duration(c.Timeouts.Status) * time.Second))
			raw, err := bufio.NewReader(io.LimitReader(conn, 256)).ReadString('\n')
			if err != nil {
				conn.Close()
				continue
			}
			switch {
			case raw == "snapshot "+c.digest+"\n":
				json.NewEncoder(conn).Encode(r)
			case raw == "status "+c.digest+"\n":
				if r.State == "running" {
					fmt.Fprintln(conn, "ok")
				} else {
					fmt.Fprintln(conn, r.Reason)
				}
			case strings.HasPrefix(raw, "stop "+c.digest+" "):
				token := strings.TrimSuffix(strings.TrimPrefix(raw, "stop "+c.digest+" "), "\n")
				until, e := runtimebudget.DecodeDeadline(token)
				if e != nil {
					conn.Close()
					continue
				}
				conn.SetDeadline(until)
				clean = true
				cancel()
				if child != nil {
					err = child.Stop(time.Duration(c.Timeouts.Stop) * time.Second / 2)
				}
				if err == nil {
					fmt.Fprintln(conn, "ok")
					_ = os.Remove(filepath.Join(c.dir, "daemon.pid"))
				} else {
					fmt.Fprintln(conn, "failed")
				}
				conn.Close()
				return err
			default:
				fmt.Fprintln(conn, "refused")
			}
			conn.Close()
		}
	}
}

func validateDependencies(d *dependencyPolicy) error {
	if d == nil {
		return nil
	}
	if len(d.DNS) > 16 || len(d.TCP) > 16 {
		return errors.New("too many daemon dependencies")
	}
	for _, host := range d.DNS {
		if host == "" || len(host) > 253 || strings.ContainsAny(host, " /:\\\r\n") {
			return errors.New("invalid DNS dependency")
		}
	}
	for _, address := range d.TCP {
		host, port, err := net.SplitHostPort(address)
		number, e := strconv.Atoi(port)
		if err != nil || e != nil || host == "" || number < 1 || number > 65535 {
			return errors.New("invalid TCP dependency")
		}
	}
	return nil
}
