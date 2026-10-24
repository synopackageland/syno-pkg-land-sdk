// Package-account supervisor for one fixed foreground executable. The pidfile
// is inventory, never authority for kill(pid). Only the parent signals its child.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type deadlines struct {
	Start  int `json:"start"`
	Stop   int `json:"stop"`
	Status int `json:"status"`
	// Uninstall bounds uninstall_args separately from Stop: a revoke on a
	// loaded NAS can take longer than stopping the daemon.
	Uninstall int `json:"uninstall"`
}
type config struct {
	Restart          *restartPolicy    `json:"restart,omitempty"`
	OperationSeconds int               `json:"operation_seconds,omitempty"`
	Network          *networkPolicy    `json:"network,omitempty"`
	Package          string            `json:"package"`
	Exec             string            `json:"exec"`
	Args             []string          `json:"args"`
	UninstallArgs    []string          `json:"uninstall_args,omitempty"`
	Env              map[string]string `json:"env"`
	Timeouts         deadlines         `json:"timeouts"`
}
type record struct {
	State      string    `json:"state,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Restarts   int       `json:"restarts,omitempty"`
	RetryAt    time.Time `json:"retry_at,omitempty"`
	Supervisor int       `json:"supervisor"`
	Child      int       `json:"child"`
	Birth      string    `json:"birth"`
	Digest     string    `json:"digest"`
}
type controller struct {
	acquireGeneration string
	config
	dest, data, dir, digest string
	installedInfo           string
}

func main() {
	if len(os.Args) != 4 {
		os.Exit(1)
	}
	c, err := load(os.Args[2], os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rc, err := c.run(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(rc)
}

func load(dest, data string) (*controller, error) {
	if os.Geteuid() == 0 || os.Getuid() != os.Geteuid() {
		return nil, errors.New("daemon requires a non-root package account")
	}
	if !filepath.IsAbs(dest) || !filepath.IsAbs(data) {
		return nil, errors.New("daemon paths must be absolute")
	}
	raw, err := os.ReadFile(filepath.Join(dest, "daemon/config.json"))
	if err != nil || len(raw) > 512*1024 {
		return nil, errors.New("invalid daemon config")
	}
	var cfg config
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		return nil, err
	}
	account, err := user.Lookup(cfg.Package)
	if err != nil || account.Uid != strconv.Itoa(os.Geteuid()) {
		return nil, errors.New("daemon requires its configured package account")
	}
	if cfg.Exec == "" || filepath.IsAbs(cfg.Exec) || filepath.Clean(cfg.Exec) != cfg.Exec || strings.HasPrefix(cfg.Exec, "../") {
		return nil, errors.New("invalid fixed executable")
	}
	if cfg.OperationSeconds < 0 || cfg.OperationSeconds > 3600 {
		return nil, errors.New("invalid operation budget")
	}
	if err := validateDeadlines(cfg); err != nil {
		return nil, err
	}
	cfg.Restart, err = normalizeRestart(cfg.Restart)
	if err != nil {
		return nil, err
	}
	if cfg.Restart != nil && cfg.Restart.Mode != "off" && cfg.Network != nil {
		return nil, errors.New("daemon restart policy requires a native daemon without Compose network supervision")
	}
	hash := sha256.Sum256(raw)
	c := &controller{config: cfg, dest: dest, data: data, dir: filepath.Join(data, "daemon"), digest: hex.EncodeToString(hash[:])}
	if err := os.Mkdir(c.dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	st, err := os.Lstat(c.dir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 || !owned(st) {
		return nil, errors.New("foreign daemon state directory")
	}
	tmp := filepath.Join(c.dir, "tmp")
	if err := os.Mkdir(tmp, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	st, err = os.Lstat(tmp)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 || !owned(st) {
		return nil, errors.New("foreign daemon temporary directory")
	}
	return c, nil
}

func owned(st os.FileInfo) bool {
	s, ok := st.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}

func safeFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || !owned(st) || st.Sys().(*syscall.Stat_t).Nlink != 1 {
		f.Close()
		return nil, errors.New("foreign daemon file")
	}
	return f, nil
}

func (c *controller) lock(name string, until time.Time) (*os.File, error) {
	f, err := safeFile(filepath.Join(c.dir, name))
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) || !time.Now().Before(until) {
			f.Close()
			return nil, errors.New("daemon lock busy")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (c *controller) run(action string) (int, error) {
	if action == "supervise" {
		return 1, c.supervise()
	}
	budget := c.Timeouts.Status
	if action == "start" {
		budget = c.Timeouts.Start
	}
	if action == "stop" || action == "restart" || action == "uninstall" {
		budget = c.Timeouts.Stop
	}
	if action == "restart" {
		budget += c.Timeouts.Start
	}
	if action == "uninstall" {
		budget += c.Timeouts.Uninstall
	}
	if action != "start" && action != "stop" && action != "status" && action != "status-json" && action != "restart" && action != "uninstall" && action != "acquire" && action != "release" {
		return 1, errors.New("unsupported daemon action")
	}
	until := time.Now().Add(time.Duration(budget) * time.Second)
	lock, err := c.lock("command.lock", until)
	if err != nil {
		return 1, err
	}
	defer lock.Close()
	if action == "status-json" {
		return c.statusJSON(until)
	}
	if action == "acquire" || action == "release" {
		err := c.recordAcquire(action == "acquire")
		if err != nil {
			return 1, err
		}
		return 0, nil
	}
	if action == "status" {
		if err := c.request("status", until); err == nil {
			return 0, nil
		} else if err.Error() == "DAEMON_RESTART_BACKOFF" || err.Error() == "DAEMON_RESTART_STARTING" {
			return 1, err
		}
		if _, err := os.Lstat(filepath.Join(c.dir, "daemon.pid")); os.IsNotExist(err) {
			owner, err := c.lock("owner.lock", time.Now())
			if err == nil {
				owner.Close()
				return 3, nil
			}
		}
		return 1, errors.New(c.savedReason())
	}
	if action == "stop" || action == "restart" || action == "uninstall" {
		stopUntil := time.Now().Add(time.Duration(c.Timeouts.Stop) * time.Second)
		if until.Before(stopUntil) {
			stopUntil = until
		}
		if err := c.stop(stopUntil); err != nil {
			return 1, err
		}
		if action == "stop" {
			return 0, nil
		}
		if action == "uninstall" {
			if len(c.UninstallArgs) == 0 {
				return 0, nil
			}
			if err := c.uninstallUntil(until); err != nil {
				return 1, err
			}
			return 0, nil
		}
	}
	startUntil := minDeadline(until, time.Now().Add(time.Duration(c.Timeouts.Start)*time.Second))
	if err := c.request("status", minDeadline(startUntil, time.Now().Add(c.probeBudget()))); err == nil {
		return 0, nil
	}
	if err := c.start(startUntil); err != nil {
		return 1, err
	}
	return 0, nil
}

func (c *controller) readRecord() (record, error) {
	var r record
	f, err := safeFileExisting(filepath.Join(c.dir, "daemon.pid"))
	if err != nil {
		return r, err
	}
	defer f.Close()
	err = json.NewDecoder(io.LimitReader(f, 4096)).Decode(&r)
	if err == nil && (r.Supervisor < 1 || r.Child < 1 || r.Digest != c.digest) {
		err = errors.New("daemon inventory mismatch")
	}
	return r, err
}

func safeFileExisting(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !owned(st) || st.Mode().Perm() != 0600 || st.Size() > 4096 || st.Sys().(*syscall.Stat_t).Nlink != 1 {
		f.Close()
		return nil, errors.New("foreign daemon record")
	}
	return f, nil
}

func (c *controller) request(action string, until time.Time) error {
	r, err := c.readRecord()
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", filepath.Join(c.dir, "ctl.sock"), time.Until(until))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := checkPeer(conn.(*net.UnixConn), r.Supervisor); err != nil {
		return err
	}
	conn.SetDeadline(until)
	token := ""
	if action == "stop" {
		encoded, e := runtimebudget.EncodeDeadline(until)
		if e != nil {
			return e
		}
		token = " " + encoded
	}
	if _, err := fmt.Fprintf(conn, "%s %s%s\n", action, c.digest, token); err != nil {
		return err
	}
	raw, err := bufio.NewReader(io.LimitReader(conn, 4096)).ReadString('\n')
	if err != nil {
		return err
	}
	if raw != "ok\n" && !(action == "status" && statusRunning(raw)) {
		if action == "snapshot" {
			var snapshot record
			if json.Unmarshal([]byte(raw), &snapshot) == nil && snapshot.Digest == c.digest && snapshot.Supervisor == r.Supervisor {
				json.NewEncoder(os.Stdout).Encode(snapshot)
				if snapshot.State != "running" {
					return &snapshotError{}
				}
				return nil
			}
		}
		if raw == "DAEMON_RESTART_BACKOFF\n" || raw == "DAEMON_RESTART_STARTING\n" {
			return errors.New(strings.TrimSpace(raw))
		}
		return errors.New("daemon operation refused")
	}
	return nil
}

// With no supervisor lock held, never signal a PID read from inventory. A live
// witnessed child means cleanup-required. Dead/zombie/reused PIDs can be retired.
func (c *controller) retired() error {
	r, err := c.readRecord()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && recordedChildAlive(r.Child, r.Birth) {
		return errors.New("daemon cleanup-required: live child without supervisor")
	}
	if err != nil {
		if _, socketErr := os.Lstat(filepath.Join(c.dir, "ctl.sock")); !os.IsNotExist(socketErr) {
			return errors.New("foreign daemon socket")
		}
	}
	for _, name := range []string{"daemon.pid", "ctl.sock"} {
		if err := os.Remove(filepath.Join(c.dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (c *controller) stop(until time.Time) error {
	// Fence automatic restarts before dispatch, including a crash/backoff race.
	// Only an explicit start under command.lock and owner.lock clears this gate.
	launch, err := c.lock("launch.lock", until)
	if err != nil {
		return err
	}
	gate, err := safeFile(filepath.Join(c.dir, "stop.request"))
	if err != nil {
		launch.Close()
		return err
	}
	err = gate.Sync()
	gate.Close()
	launch.Close()
	if err != nil {
		return err
	}
	if err := c.request("stop", until); err != nil {
		owner, lockErr := c.lock("owner.lock", time.Now())
		if lockErr != nil {
			return err
		}
		defer owner.Close()
		return c.retired()
	}
	owner, err := c.lock("owner.lock", until)
	if err != nil {
		return err
	}
	defer owner.Close()
	return c.retired()
}

func (c *controller) start(until time.Time) error {
	if !time.Now().Before(until) {
		return errors.New("daemon start timeout")
	}
	owner, err := c.lock("owner.lock", time.Now())
	if err != nil {
		return err
	}
	if err := c.retired(); err != nil {
		owner.Close()
		return err
	}
	if err := os.Remove(filepath.Join(c.dir, "stop.request")); err != nil && !os.IsNotExist(err) {
		owner.Close()
		return err
	}
	// Pass the already-held lifetime lock to the new supervisor. No handoff gap.
	log, err := safeFile(filepath.Join(c.dir, "daemon.log"))
	if err != nil {
		owner.Close()
		return err
	}
	defer log.Close()
	if _, err := log.Seek(0, io.SeekEnd); err != nil {
		owner.Close()
		return err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		owner.Close()
		return err
	}
	defer reader.Close()
	self, err := os.Executable()
	if err != nil {
		owner.Close()
		writer.Close()
		return err
	}
	cmd := exec.Command(self, "supervise", c.dest, c.data)
	token, err := runtimebudget.EncodeDeadline(until)
	if err != nil {
		owner.Close()
		writer.Close()
		return err
	}
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SYNOPKGLAND_START_DEADLINE=" + token}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, log, log
	cmd.ExtraFiles = []*os.File{writer, owner}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	writer.Close()
	owner.Close()
	if err != nil {
		return err
	}
	// Wait in a goroutine to reap if startup fails. Successful supervisors are
	// detached when this command exits; their directly-owned child stays attached.
	go cmd.Wait()
	ready := make(chan error, 1)
	go func() {
		raw, err := bufio.NewReader(io.LimitReader(reader, 256)).ReadString('\n')
		if err == nil && raw != "ready\n" {
			if strings.HasPrefix(raw, "COMPOSE_STARTUP_TIMEOUT:") || raw == "COMPOSE_STARTUP_INTERRUPTED\n" {
				err = errors.New(strings.TrimSpace(raw))
			} else {
				err = errors.New("daemon startup refused; inspect daemon/daemon.log")
			}
		} else if err != nil {
			err = errors.New("daemon startup failed; inspect daemon/daemon.log")
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err == nil && !time.Now().Before(until) {
			cmd.Process.Signal(syscall.SIGTERM)
			return errors.New("daemon start timeout")
		}
		return err
	case <-time.After(time.Until(until)):
		// Only our own still-retained supervisor can be signalled, never inventory.
		cmd.Process.Signal(syscall.SIGTERM)
		return errors.New("daemon start timeout")
	}
}

func (c *controller) expand(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, "${SYNOPKG_PKGDEST}", c.dest), "${SYNOPKG_PKGVAR}", c.data)
}

func (c *controller) child(args []string) *exec.Cmd {
	expanded := make([]string, len(args))
	for i, v := range args {
		expanded[i] = c.expand(v)
	}
	cmd := exec.Command(filepath.Join(c.dest, "daemon/payload", c.Exec), expanded...)
	cmd.Dir = c.data
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + c.data, "TMPDIR=" + filepath.Join(c.dir, "tmp")}
	keys := make([]string, 0, len(c.Env))
	for key := range c.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+c.expand(c.Env[key]))
	}
	if c.Network != nil {
		cmd.Env = append(cmd.Env, "SYNOPKGLAND_DAEMON_SOCKET="+filepath.Join(c.dir, "ctl.sock"), "SYNOPKGLAND_DAEMON_DIGEST="+c.digest, "SYNOPKGLAND_DAEMON_STATUS_SECONDS="+strconv.Itoa(c.Timeouts.Status))
	}
	cmd.SysProcAttr = childAttributes()
	return cmd
}

func (c *controller) uninstall() error {
	return c.uninstallUntil(time.Now().Add(time.Duration(c.Timeouts.Uninstall) * time.Second))
}
func (c *controller) uninstallUntil(until time.Time) error {
	until = minDeadline(until, time.Now().Add(time.Duration(c.Timeouts.Uninstall)*time.Second))
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd := c.child(c.UninstallArgs)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func(child *exec.Cmd, result chan<- error) { result <- child.Wait() }(cmd, done)
	select {
	case err := <-done:
		return err
	case <-time.After(runtimebudget.Inner(time.Until(until))):
		cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-done:
		return errors.New("daemon uninstall timeout")
	case <-time.After(min(runtimebudget.Cleanup, max(0, time.Until(until)/2))):
		cmd.Process.Kill()
	}
	select {
	case <-done:
		return errors.New("daemon uninstall timeout")
	case <-time.After(max(0, time.Until(until))):
		return errors.New("daemon uninstall cleanup-required")
	}
}

func (c *controller) supervise() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ready, owner := os.NewFile(3, "ready"), os.NewFile(4, "owner.lock")
	if ready == nil || owner == nil {
		return errors.New("missing supervisor bootstrap")
	}
	defer ready.Close()
	defer owner.Close()
	syscall.CloseOnExec(3)
	syscall.CloseOnExec(4)
	// These descriptors must be the private pipe and exactly our already-held lock.
	st, err := owner.Stat()
	if err != nil {
		return err
	}
	pathStat, err := os.Lstat(filepath.Join(c.dir, "owner.lock"))
	if err != nil || !os.SameFile(st, pathStat) || !owned(st) {
		return errors.New("invalid supervisor lock")
	}
	pipeStat, err := ready.Stat()
	if err != nil || pipeStat.Mode()&os.ModeNamedPipe == 0 {
		return errors.New("invalid supervisor pipe")
	}
	if err := syscall.Flock(int(owner.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	until, err := runtimebudget.DecodeDeadline(os.Getenv("SYNOPKGLAND_START_DEADLINE"))
	if err != nil || !time.Now().Before(until) {
		return errors.New("daemon start deadline missing or exhausted")
	}
	// Process stability and handoff are inside the caller's original deadline.
	if c.Network != nil {
		readinessUntil := until.Add(-time.Second - min(runtimebudget.Cleanup, time.Until(until)/10))
		err := waitReadiness(readinessUntil, signals, func() error {
			return c.checkReadinessUntil(minDeadline(readinessUntil, time.Now().Add(c.probeBudget())))
		})
		if err != nil {
			fmt.Fprintln(ready, err)
			return err
		}
		c.rememberImages()
	}
	cmd := c.child(c.Args)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func(child *exec.Cmd, result chan<- error) { result <- child.Wait() }(cmd, done)
	stopUntil := time.Time{}
	stop := func() error {
		if done == nil {
			return nil
		}
		if stopUntil.IsZero() {
			stopUntil = time.Now().Add(time.Duration(c.Timeouts.Stop) * time.Second)
		}
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
			return nil
		case <-time.After(runtimebudget.Inner(time.Until(stopUntil))):
			cmd.Process.Kill()
		}
		select {
		case <-done:
			return nil
		case <-time.After(max(0, time.Until(stopUntil))):
			return errors.New("daemon child stop timeout")
		}
	}
	// Process stability is the readiness contract; it is not app HTTP readiness.
	select {
	case <-done:
		return errors.New("daemon exited during startup")
	case <-signals:
		return stop()
	case <-time.After(min(time.Second, max(0, time.Until(until)))):
	}
	if !time.Now().Before(until) {
		stop()
		return errors.New("daemon start timeout")
	}
	r := record{Supervisor: os.Getpid(), Child: cmd.Process.Pid, Birth: processBirth(cmd.Process.Pid), Digest: c.digest, State: "running"}
	err = c.writeRecord(r)
	if err != nil {
		stop()
		return err
	}
	socket := filepath.Join(c.dir, "ctl.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		stop()
		os.Remove(filepath.Join(c.dir, "daemon.pid"))
		return err
	}
	ln.SetUnlinkOnClose(false)
	defer ln.Close()
	clean := false
	defer func() {
		os.Remove(socket)
		if clean {
			os.Remove(filepath.Join(c.dir, "daemon.pid"))
		}
	}()
	if err := os.Chmod(socket, 0600); err != nil {
		stop()
		return err
	}
	if !time.Now().Before(until) {
		clean = stop() == nil
		return errors.New("daemon start timeout")
	}
	if _, err := ready.WriteString("ready\n"); err != nil {
		clean = stop() == nil
		return err
	}
	ready.Close()
	connections := make(chan *net.UnixConn)
	go func() {
		for {
			conn, err := ln.AcceptUnix()
			if err != nil {
				return
			}
			select {
			case connections <- conn:
			case <-time.After(runtimebudget.Cleanup):
				conn.Close()
			}
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	healthy := true
	var health healthWindow
	results := make(chan error, 1)
	probing := false
	var probeStarted time.Time
	// Status must wait for the active probe instead of turning every routine
	// poll into a brief false outage.
	type statusWaiter struct {
		conn  *net.UnixConn
		timer *time.Timer
	}
	var statusWaiters []statusWaiter
	defer func() {
		for _, w := range statusWaiters {
			w.timer.Stop()
			w.conn.Close()
		}
	}()
	// A probe can outlast DSM's status budget. Answer just before that budget
	// with the last completed result rather than turning a slow probe into an outage.
	answered := make(chan *net.UnixConn, 64)
	queueStatus := func(conn *net.UnixConn) {
		timer := time.AfterFunc(time.Duration(c.Timeouts.Status)*time.Second*3/4, func() { answered <- conn })
		statusWaiters = append(statusWaiters, statusWaiter{conn, timer})
	}
	flushStatus := func(line []byte) {
		for _, w := range statusWaiters {
			w.timer.Stop()
			w.conn.Write(line)
			w.conn.Close()
		}
		statusWaiters = nil
	}
	budget := restartBudget{policy: c.Restart}
	var restartTimer, stableTimer *time.Timer
	var restartC, stableC <-chan time.Time
	defer func() {
		if restartTimer != nil {
			restartTimer.Stop()
		}
		if stableTimer != nil {
			stableTimer.Stop()
		}
	}()
	childExited := func(exitErr error) (bool, error) {
		done = nil // The child has been reaped; stop must not wait on it again.
		healthy = false
		// A dead child is not starting; let DSM fall back to the saved reason.
		flushStatus([]byte("unknown\n"))
		stableC = nil
		if stableTimer != nil {
			stableTimer.Stop()
		}
		r.RetryAt = time.Time{}
		if exitErr == nil {
			r.State, r.Reason = "exited", "DAEMON_CHILD_EXIT"
			fmt.Fprintln(os.Stderr, r.Reason)
			return false, c.writeRecord(r)
		}
		r.State, r.Reason = "failed", "DAEMON_CHILD_FAILED"
		if delay, ok := budget.next(time.Now()); ok {
			r.Restarts++
			r.State, r.Reason = "restarting", "DAEMON_RESTART_BACKOFF"
			r.RetryAt = time.Now().Add(delay)
			fmt.Fprintf(os.Stderr, "%s attempt=%d delay=%s\n", r.Reason, r.Restarts, delay)
			if err := c.writeRecord(r); err != nil {
				return false, err
			}
			restartTimer = time.NewTimer(delay)
			restartC = restartTimer.C
			return true, nil
		}
		if c.Restart != nil && c.Restart.Mode == "on-failure" {
			r.Reason = "DAEMON_RESTART_LIMIT"
		}
		fmt.Fprintf(os.Stderr, "%s attempts=%d; manual start required\n", r.Reason, r.Restarts)
		if err := c.writeRecord(r); err != nil {
			return false, err
		}
		return false, errors.New(r.Reason)
	}
	for {
		// Give a queued lifecycle stop priority over an expired backoff timer.
		select {
		case <-signals:
			err := stop()
			clean = err == nil
			return err
		default:
		}
		select {
		case <-restartC:
			restartC = nil
			select {
			case <-signals:
				clean = true
				return nil
			default:
			}
			launch, err := c.lock("launch.lock", time.Now().Add(time.Duration(c.Timeouts.Stop)*time.Second))
			if err != nil {
				return err
			}
			if _, err := os.Lstat(filepath.Join(c.dir, "stop.request")); !os.IsNotExist(err) {
				launch.Close()
				clean = true
				return nil
			}
			cmd = c.child(c.Args)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			err = cmd.Start()
			launch.Close()
			if err != nil {
				if again, err := childExited(err); !again {
					return err
				}
				continue
			}
			done = make(chan error, 1)
			// Capture each generation rather than the mutable loop variables.
			go func(child *exec.Cmd, result chan<- error) { result <- child.Wait() }(cmd, done)
			r.Child, r.Birth = cmd.Process.Pid, processBirth(cmd.Process.Pid)
			r.State, r.Reason, r.RetryAt = "starting", "DAEMON_RESTART_STARTING", time.Time{}
			if err := c.writeRecord(r); err != nil {
				stop()
				return err
			}
			stableTimer = time.NewTimer(time.Second)
			stableC = stableTimer.C
		case <-stableC:
			stableC = nil
			// A child that died before the stability timer is never reported ready.
			select {
			case err := <-done:
				if again, err := childExited(err); !again {
					return err
				}
				continue
			default:
			}
			healthy = true
			r.State, r.Reason = "running", ""
			if err := c.writeRecord(r); err != nil {
				stop()
				return err
			}
			fmt.Fprintf(os.Stderr, "DAEMON_RESTART_RECOVERED attempts=%d\n", r.Restarts)
		case <-tick.C:
			if done != nil && stableC == nil && !probing {
				probing = true
				probeStarted = time.Now()
				probeUntil := probeStarted.Add(c.probeBudget())
				if !health.failedAt.IsZero() {
					probeUntil = minDeadline(probeUntil, health.failedAt.Add(c.recoveryBudget()))
				}
				go func() { results <- c.checkReadinessUntil(probeUntil) }()
			}
		case err := <-results:
			probing = false
			if err != nil && health.failedAt.IsZero() {
				health.failedAt = probeStarted
			}
			if health.observe(time.Now(), err, c.recoveryBudget()) {
				clean = stop() == nil
				if !transientReadiness(err) {
					return errors.New(readinessPhase(err))
				}
				return recoveryFailure(err, c.recoveryBudget())
			}
			healthy = err == nil
			flushStatus([]byte(statusLine(healthy)))
		case conn := <-answered:
			for i, w := range statusWaiters {
				if w.conn == conn {
					statusWaiters = append(statusWaiters[:i], statusWaiters[i+1:]...)
					conn.Write([]byte(statusLine(healthy)))
					conn.Close()
					break
				}
			}
		case err := <-done:
			if again, err := childExited(err); !again {
				return err
			}
		case <-signals:
			err := stop()
			clean = err == nil
			return err
		case conn := <-connections:
			conn.SetDeadline(time.Now().Add(time.Duration(c.Timeouts.Status) * time.Second))
			raw, err := bufio.NewReader(io.LimitReader(conn, 256)).ReadString('\n')
			if err != nil {
				conn.Close()
				continue
			}
			if raw == "snapshot "+c.digest+"\n" || raw == "status "+c.digest+"\n" {
				select {
				case exitErr := <-done:
					again, exitErr := childExited(exitErr)
					if !again {
						if strings.HasPrefix(raw, "snapshot ") {
							json.NewEncoder(conn).Encode(r)
						} else {
							fmt.Fprintln(conn, "failed")
						}
						conn.Close()
						return exitErr
					}
				default:
				}
			}
			if raw == "snapshot "+c.digest+"\n" {
				snapshot := r
				if snapshot.State == "running" && !healthy {
					snapshot.State, snapshot.Reason = "unavailable", "DAEMON_READINESS_PENDING"
				}
				json.NewEncoder(conn).Encode(snapshot)
				conn.Close()
			} else if raw == "status "+c.digest+"\n" {
				if r.State == "restarting" || r.State == "starting" {
					fmt.Fprintln(conn, r.Reason)
					conn.Close()
				} else {
					if probing && len(statusWaiters) < 64 {
						queueStatus(conn)
						continue
					}
					conn.Write([]byte(statusLine(healthy)))
					conn.Close()
				}
			} else if strings.HasPrefix(raw, "stop "+c.digest+" ") {
				token := strings.TrimSuffix(strings.TrimPrefix(raw, "stop "+c.digest+" "), "\n")
				deadline, e := runtimebudget.DecodeDeadline(token)
				if e != nil {
					conn.Close()
					continue
				}
				stopUntil = minDeadline(deadline, time.Now().Add(time.Duration(c.Timeouts.Stop)*time.Second))
				conn.SetDeadline(stopUntil)
				err := stop()
				clean = err == nil
				if err == nil {
					conn.Write([]byte("ok\n"))
				} else {
					conn.Write([]byte("failed\n"))
				}
				conn.Close()
				return err
			} else {
				conn.Write([]byte("refused\n"))
				conn.Close()
			}
		}
	}
}

// The proc parser handles spaces and ')' inside Linux executable names.
func parseBirth(raw string) (string, bool) {
	end := strings.LastIndex(raw, ")")
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(raw[end+1:])
	if len(fields) < 20 {
		return "", false
	}
	_, err := strconv.ParseUint(fields[19], 10, 64)
	return fields[19], err == nil && fields[0] != "Z" && fields[0] != "X"
}

func minDeadline(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func (c *controller) operationBudget() time.Duration {
	if c.OperationSeconds > 0 && c.OperationSeconds <= runtimebudget.MaxSeconds {
		return time.Duration(c.OperationSeconds) * time.Second
	}
	return runtimebudget.Operation
}
