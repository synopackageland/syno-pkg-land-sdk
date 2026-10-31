package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/budget"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const stateBase = "/usr/local/etc/synopkgland"
const socketBase = "/run/synopkgland"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "executor:", err)
		os.Exit(exitStatus(err))
	}
}
func run(args []string) error {
	if len(args) > 0 && args[0] == "scheduler-read" {
		return runSchedulerRead(args[1:])
	}
	if len(args) > 0 && args[0] == "root-exec" {
		return runRootExec(args[1:])
	}
	if len(args) == 1 {
		if args[0] == "move-child" {
			return runMoveChild()
		}
		if args[0] == "read-child" {
			return runReadChild()
		}
		if args[0] == "part-cleanup-child" {
			return runPartCleanupChild()
		}
		if args[0] == "source-cleanup-child" {
			return runSourceCleanupChild()
		}
	}
	if len(args) > 0 && args[0] == "check-policy" {
		return checkPolicy(args[1:])
	}
	if len(args) < 1 {
		return errors.New("usage: executor serve|supervise --package <pkg> | control start|stop|purge --package <pkg> | check-policy --package <pkg> < policy.json")
	}
	mode := args[0]
	args = args[1:]
	action := ""
	if mode == "control" {
		if len(args) == 0 {
			return errors.New("control action required")
		}
		action = args[0]
		args = args[1:]
		if !contains([]string{"start", "stop", "purge"}, action) {
			return errors.New("invalid control action")
		}
	} else if mode != "serve" && mode != "supervise" && mode != "select-scope" && mode != "install-root" {
		return errors.New("invalid command")
	}
	fs := flag.NewFlagSet("executor", flag.ContinueOnError)
	pkg := fs.String("package", "", "package account")
	scope := fs.String("scope", "", "administrator-confirmed scope")
	actor := fs.String("actor", "", "password-confirmed administrator")
	expectedGeneration := fs.Uint64("expected-generation", maxScopeGeneration+1, "protected generation observed before confirmation")
	revived := fs.Bool("revived", false, "restarted by the root supervisor")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *revived && mode != "serve" {
		return errors.New("unexpected revive")
	}
	if fs.NArg() != 0 || !tokenRE.MatchString(*pkg) {
		return errors.New("invalid package")
	}
	if os.Geteuid() != 0 {
		return errors.New("root required")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	if mode == "control" && action != "start" {
		return controlStop(*pkg, action == "purge", filepath.Dir(exe))
	}
	e, uid, gid, err := load(*pkg, exe)
	if err != nil {
		return err
	}
	if mode == "select-scope" {
		if e.Policy.Scope != installSelectedScope || !validSelectedScope(*scope) || *actor == "" || *expectedGeneration > maxScopeGeneration {
			return errors.New("scope selection")
		}
		return rootSelectScope(*pkg, *scope, *actor, *expectedGeneration)
	}
	if *scope != "" || *actor != "" || *expectedGeneration != maxScopeGeneration+1 {
		return errors.New("unexpected selection arguments")
	}
	if mode == "install-root" {
		return installRootPrograms(filepath.Join("/var/packages", *pkg, "target/root"), rootPrograms{Dir: filepath.Dir(exe)}, e.Policy.Root)
	}
	if mode == "control" {
		// If the validated service already owns its pidfile, start is idempotent.
		f, err := os.OpenFile(filepath.Join(e.Store.Dir, "executor.pid"), os.O_RDWR|os.O_CREATE, 0600)
		if err != nil {
			return err
		}
		err = lockFile(f)
		f.Close()
		if err != nil {
			f, openErr := os.OpenFile(filepath.Join(e.Store.Dir, "executor.pid"), os.O_RDWR, 0600)
			if openErr != nil {
				return openErr
			}
			err = stopApprovedProcess(f, filepath.Dir(exe))
			closeErr := f.Close() // Release the pid lock before the replacement child can run.
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		cmd := supervisorCommand(exe, *pkg)
		log, err := os.OpenFile(filepath.Join(e.Store.Dir, "executor.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		defer log.Close()
		cmd.Stdout = log
		cmd.Stderr = log
		if err = cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	if mode == "supervise" {
		return runSupervisor(exe, *pkg)
	}
	return serve(e, uid, gid, *revived)
}

// Never chmod or chown an existing untrusted directory into trust. Check all
// ancestors first; package-writable symlinks and directories fail closed.
func secureDir(path string, mode os.FileMode) error {
	if !cleanPath(path) {
		return errors.New("directory path")
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	current := "/"
	for i, p := range parts {
		current = filepath.Join(current, p)
		want := os.FileMode(0755)
		if i == len(parts)-1 {
			want = mode
		}
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			info, err = createDirectory(current, want, 0, 0)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || !ownedByRoot(info) || info.Mode().Perm()&0022 != 0 {
			return errors.New("untrusted directory")
		}
		if i == len(parts)-1 && info.Mode().Perm() != mode {
			return errors.New("directory mode")
		}
	}
	return nil
}
func trustedTree(path string) error {
	return filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !ownedByRoot(info) || info.Mode().Perm()&0022 != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.New("untrusted runtime")
		}
		return nil
	})
}
func load(pkg, exe string) (*Executor, int, int, error) {
	dir := filepath.Dir(exe)
	if dir != filepath.Join(stateBase, pkg, "runtime") {
		return nil, 0, 0, errors.New("runtime must be in package protected state/runtime")
	}
	if err := secureDir(dir, 0700); err != nil {
		return nil, 0, 0, err
	}
	if err := trustedTree(dir); err != nil {
		return nil, 0, 0, err
	}
	f, err := openRegular(filepath.Join(dir, "policy.json"))
	if err != nil {
		return nil, 0, 0, err
	}
	b, err := readLimited(f, maxRequest)
	f.Close()
	if err != nil {
		return nil, 0, 0, err
	}
	p, err := parsePolicy(b, pkg)
	if err != nil {
		return nil, 0, 0, err
	}
	u, err := user.Lookup(pkg)
	if err != nil {
		return nil, 0, 0, errors.New("package account")
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil || uid <= 0 {
		return nil, 0, 0, errors.New("package uid")
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil || gid <= 0 {
		return nil, 0, 0, errors.New("package gid")
	}
	state := filepath.Join(stateBase, pkg, "delegations")
	if err = secureDir(filepath.Join(stateBase, pkg), 0700); err != nil {
		return nil, 0, 0, err
	}
	if err = secureDir(state, 0700); err != nil {
		return nil, 0, 0, err
	}
	ed, err := digestFile(context.Background(), exe, maxExecutorBytes)
	if err != nil {
		return nil, 0, 0, err
	}
	pd, err := digestFile(context.Background(), filepath.Join(dir, "policy.json"), maxPolicyBytes)
	if err != nil {
		return nil, 0, 0, err
	}
	dsm := newDSM(filepath.Join(stateBase, pkg))
	staging := subprocessWriter{Staging: filepath.Join("/var/packages", pkg, "var/executor-staging"), Executable: exe, PackageUID: uid, PackageGID: gid}
	e := &Executor{Policy: p, DSM: dsm, Tasks: dsm, Writer: staging, Reader: staging, Runner: rootPrograms{Dir: dir, Executable: exe}, RootStaging: staging, Store: &Store{Dir: state}, Now: time.Now, ExecutorDigest: ed, PolicyDigest: pd}
	if p.Portal != nil {
		e.Portal = portalEntry{Package: pkg, Policy: *p.Portal, Paths: dsmPortal, DSM: dsm, StateDir: state, OperationBudget: p.budgets().Duration("operation")}
	}
	return e, uid, gid, nil
}

// parsePolicy decodes and validates the approved policy of pkg.
func parsePolicy(b []byte, pkg string) (Policy, error) {
	var p Policy
	err := strictJSON(b, &p)
	if err == nil {
		err = validatePolicy(p, pkg)
	}
	if err != nil {
		return Policy{}, fmt.Errorf("invalid policy: %w", err)
	}
	return p, nil
}

// checkPolicy validates the policy on stdin as load() would, without root,
// so a build can refuse a policy the executor would refuse.
func checkPolicy(args []string) error {
	fs := flag.NewFlagSet("executor check-policy", flag.ContinueOnError)
	pkg := fs.String("package", "", "package account")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !tokenRE.MatchString(*pkg) {
		return errors.New("invalid package")
	}
	b, err := readLimited(os.Stdin, maxRequest)
	if err != nil {
		return fmt.Errorf("invalid policy: %w", err)
	}
	_, err = parsePolicy(b, *pkg)
	return err
}
func validatePolicy(p Policy, pkg string) error {
	if p.Timeouts != nil && !p.Timeouts.Valid() {
		return errors.New("policy timeouts")
	}
	if !validWebAPIProfiles(p) {
		return errors.New("policy webapi")
	}
	if p.Version != 1 || p.Package != pkg || !tokenRE.MatchString(p.App) || len(p.Operations) == 0 {
		return errors.New("policy")
	}
	if !contains([]string{"", "admin-only", "app-privilege"}, p.Entry) {
		return errors.New("policy entry")
	}
	if p.Lifetime != "" && p.Lifetime != "until-revoked" {
		return errors.New("policy lifetime")
	}
	if (len(p.Destinations) > 0) != (p.Lifetime == "until-revoked") {
		return errors.New("policy destinations")
	}
	keys := map[string]bool{}
	for _, key := range p.Destinations {
		if !destinationRE.MatchString(key) || keys[key] {
			return errors.New("policy destination")
		}
		keys[key] = true
	}
	if p.Lifetime != "" && !contains(p.Operations, "files.moveFromStaging") {
		return errors.New("policy move")
	}
	seen := map[string]bool{}
	for _, op := range p.Operations {
		if !contains(allOps, op) || seen[op] {
			return errors.New("policy operation")
		}
		seen[op] = true
		if strings.HasPrefix(op, "files.") && scopeVirtual(p.Scope) == "" && !(p.Scope == installSelectedScope && p.Lifetime == "until-revoked") {
			return errors.New("policy scope")
		}
	}
	// Only delegate.create makes the delegation every files op acts through.
	for op := range seen {
		if strings.HasPrefix(op, "files.") && !seen["delegate.create"] {
			return errors.New("policy operation: " + op + " needs delegate.create")
		}
	}
	if seen["delegate.create"] && len(p.WebAPI) == 0 && scopeVirtual(p.Scope) == "" && !(p.Scope == installSelectedScope && p.Lifetime == "until-revoked") {
		return errors.New("policy scope")
	}
	if (p.Portal != nil) != seen["portal.register"] || seen["portal.register"] != seen["portal.unregister"] {
		return errors.New("policy portal")
	}
	if p.Portal != nil && !validPortal(p.Package, *p.Portal) {
		return errors.New("policy portal")
	}
	for n, r := range p.Root {
		if !tokenRE.MatchString(n) || !r.valid() || (r.Run != nil && !seen["root.run"]) || (r.Job != nil && !seen["root.start"]) {
			return errors.New("policy root")
		}
	}
	if seen["root.start"] != seen["root.jobs.list"] || seen["root.start"] != seen["root.jobs.get"] || seen["root.start"] != seen["root.jobs.stop"] {
		return errors.New("policy root jobs")
	}
	for _, step := range []struct{ kind, name string }{{"start", p.Start}, {"stop", p.Stop}, {"purge", p.Purge}} {
		if err := lifecycleStep(p, step.kind, step.name); err != nil {
			return err
		}
	}
	return nil
}

// lifecycleStep checks the program a policy names as its start, stop or purge step:
// the executor refuses sessions a step, so a step that declares run or job
// would leave the App's root calls to it denied.
func lifecycleStep(p Policy, kind, name string) error {
	if name == "" {
		return nil
	}
	entry, ok := p.Root[name]
	if !ok {
		return errors.New("policy " + kind)
	}
	if entry.session() {
		return fmt.Errorf("policy %s: root program %q runs at %s and cannot also declare run or job; give the session work another program name", kind, name, kind)
	}
	return nil
}
func serve(e *Executor, uid, gid int, revived bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	actions := make(chan string, 1)
	stopControls := rootControlSignals(ctx, actions)
	defer stopControls()
	pkgDir := filepath.Join("/var/packages", e.Policy.Package)
	newMonitor := newLifecycleMonitor
	if revived {
		newMonitor = newRevivedLifecycleMonitor
	}
	monitor := newMonitor(e.Policy.Package, pkgDir, filepath.Join(pkgDir, "var/executor/control"), func(s string) { log.Print(s) })
	pidpath := filepath.Join(e.Store.Dir, "executor.pid")
	pid, err := os.OpenFile(pidpath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer pid.Close()
	if err = lockFile(pid); err != nil {
		return errAlreadyRunning
	}
	if err = pid.Truncate(0); err != nil {
		return err
	}
	if _, err = fmt.Fprintln(pid, os.Getpid()); err != nil {
		return err
	}
	if err = pid.Sync(); err != nil {
		return err
	}
	if err = secureDir(socketBase, 0755); err != nil {
		return err
	}
	sockDir := filepath.Join(socketBase, e.Policy.Package)
	info, err := os.Lstat(sockDir)
	if os.IsNotExist(err) {
		if _, err = createDirectory(sockDir, 0750, 0, gid); err != nil {
			return err
		}
	} else if err != nil || !directoryMatches(info, 0750, 0, gid) {
		return errors.New("socket directory")
	}
	sock := filepath.Join(sockDir, "executor.sock")
	if info, err = os.Lstat(sock); err == nil {
		if info.Mode()&os.ModeSocket == 0 || !ownedByRoot(info) {
			return errors.New("socket path")
		}
		if err = os.Remove(sock); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	d := e.DSM.(*liveDSM)
	runtime := filepath.Join(stateBase, e.Policy.Package, "runtime")
	removeStagedExecutors(runtime)
	// The inventory serves cleanup and the root steps' approval, never a
	// session: it is read back from DSM while the socket already listens.
	// Status reports each readback, which repeats while the executor runs.
	inventory := make(chan struct{})
	passed := true
	e.TaskCheck = &taskCheck{Now: time.Now, Base: ctx, Check: func(ctx context.Context) error {
		err := snapshotTasks(ctx, d, e.Policy.Package, e.ExecutorDigest, e.PolicyDigest, filepath.Join(pkgDir, "target/executor/setup.json"), runtime)
		if err != nil && passed {
			log.Print("task inventory snapshot unavailable; cleanup will use retained verified inventory")
		}
		passed = err == nil
		return err
	}}
	go func() {
		e.TaskCheck.run(ctx)
		close(inventory)
		e.TaskCheck.loop(ctx)
	}()
	go func() {
		for {
			a := monitor.watch(ctx, 500*time.Millisecond)
			e.setJobEnd(map[string]string{"stop": stopReason(filepath.Join(pkgDir, "var/executor/stop-reason")), "purge": "purge"}[a])
			select {
			case <-ctx.Done():
				return
			case actions <- a:
			}
			if a == "purge" {
				return
			}
		}
	}()
	target, packageInfo := filepath.Join(pkgDir, "target/executor"), filepath.Join(pkgDir, "INFO")
	open := func() (func(), error) {
		// An upgrade keeps this process when the pins stay, so each start
		// reads the version anew; sessions wait for the step.
		e.PackageVersion = packageVersion(packageInfo)
		step := rootStart{Approved: uninstallApproval(d, e.Policy.Package, target, runtime, e.ExecutorDigest, e.PolicyDigest), Runner: e.Runner, State: e.RootState, Store: e.Store}
		listen := func() (func(), error) { return openService(ctx, e, uid, gid, sock) }
		return e.openStarted(ctx, listen, inventory, step, func(s string) { log.Print(s) })
	}
	samePins := func(ctx context.Context) bool {
		// Remove stale diagnostics on a failed read. This file is never approval evidence.
		readback := filepath.Join(sockDir, "approval.json")
		if diff, err := approvalReadback(ctx, e.Policy.Package, runtime, target, packageInfo); err == nil {
			if err := writeApprovalReadback(readback, gid, diff); err != nil {
				os.Remove(readback)
			}
		} else {
			os.Remove(readback)
		}
		state := packagePins(ctx, target, packageInfo, rootOwnedReadOnly, e.ExecutorDigest, e.PolicyDigest)
		install := func() error { return installAnchored(ctx, target, packageInfo, rootOwnedReadOnly, runtime) }
		start := func() error {
			// A monitor stopping for root control start must not start another.
			if err := ctx.Err(); err != nil {
				return err
			}
			return startInstalled(runtime, e.Policy.Package)
		}
		return approvedPins(state, install, start, func(s string) { log.Print(s) })
	}
	if e.Policy.RootState {
		// Without it the policy's root programs are refused (ROOT_FAILED).
		data, err := filepath.EvalSymlinks(filepath.Join(pkgDir, "var"))
		state, err2 := rootStateFor(data, e.Policy.Package)
		if err == nil && err2 == nil {
			err = ensureRootState(state)
		}
		if err == nil && err2 == nil {
			e.RootState = state
		} else {
			log.Printf("root state unavailable: package=%s", e.Policy.Package)
		}
	}
	e.StopStep = func(reason string) {
		step := rootStop{Approved: uninstallApproval(d, e.Policy.Package, target, runtime, e.ExecutorDigest, e.PolicyDigest), Runner: e.Runner, State: e.RootState}
		e.rootMu.Lock()
		until := e.shutdownUntil
		e.rootMu.Unlock()
		if until.IsZero() {
			until = time.Now().Add(e.Policy.budgets().Duration("stop"))
		}
		stepCtx, cancel := context.WithDeadline(context.Background(), until)
		defer cancel()
		step.run(stepCtx, e.Policy, reason, func(s string) { log.Print(s) })
	}
	purge := func() error {
		// Cleanup reads the inventory this start may still be writing.
		<-inventory
		step := rootPurge{
			Approved:    uninstallApproval(d, e.Policy.Package, target, runtime, e.ExecutorDigest, e.PolicyDigest),
			Runner:      e.Runner,
			DeleteState: uninstallDeletesState(filepath.Join(pkgDir, "var/executor/uninstall")),
			State:       e.RootState,
		}
		return purgeProtected(ctx, d, e.Policy.Package, filepath.Join(stateBase, e.Policy.Package), runtime, sockDir, step)
	}
	return manageService(ctx, actions, open, samePins, purge)
}

func openService(parent context.Context, e *Executor, uid, gid int, sock string) (func(), error) {
	old := syscall.Umask(0077)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	ln.SetUnlinkOnClose(false)
	if err = os.Chown(sock, 0, gid); err == nil {
		err = os.Chmod(sock, 0660)
	}
	if err == nil {
		err = verifySocket(sock, 0, gid, 0660)
	}
	if err != nil {
		ln.Close()
		os.Remove(sock)
		return nil, err
	}
	e.rootMu.Lock()
	e.shutdownUntil = time.Time{}
	e.rootMu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	if err = e.startOperations(ctx); err != nil {
		cancel()
		ln.Close()
		os.Remove(sock)
		return nil, err
	}
	if err = e.startJobs(); err != nil {
		cancel()
		e.stopOperations()
		ln.Close()
		os.Remove(sock)
		return nil, err
	}
	var active sync.WaitGroup
	var mu sync.Mutex
	connections := map[*net.UnixConn]bool{}
	done := make(chan struct{})
	slots := make(chan struct{}, maxPendingConnections)
	go func() {
		defer close(done)
		for {
			c, err := ln.AcceptUnix()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
				mu.Lock()
				if ctx.Err() != nil {
					mu.Unlock()
					c.Close()
					<-slots
					continue
				}
				connections[c] = true
				active.Add(1)
				mu.Unlock()
				go func() {
					defer active.Done()
					defer func() { <-slots; mu.Lock(); delete(connections, c); mu.Unlock(); c.Close() }()
					e.Connection(ctx, c, uid, peerUID)
				}()
			default:
				c.Close()
			}
		}
	}()
	return func() {
		e.rootMu.Lock()
		e.shutdownUntil = time.Now().Add(e.Policy.budgets().Duration("stop"))
		e.rootMu.Unlock()
		cancel()
		ln.Close()
		<-done
		mu.Lock()
		for c := range connections {
			c.Close()
		}
		mu.Unlock()
		active.Wait()
		e.endRoot(e.jobEnd())
		e.stopOperations()
		os.Remove(sock)
	}, nil
}
func purgeProtected(ctx context.Context, d *liveDSM, pkg, state, runtime, socket string, step rootPurge) error {
	return purgeProtectedWith(ctx, d, pkg, state, runtime, socket, dsmLeftovers, step)
}

func purgeProtectedWith(ctx context.Context, d *liveDSM, pkg, state, runtime, socket string, leftovers leftoverPaths, step rootPurge) error {
	policy, policyErr := runtimePolicy(pkg, runtime)
	limits := policy.budgets()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), limits.Duration("purge"))
	defer cancel()
	ctx = withCommandTimeout(ctx, limits.Duration("operation"))
	logf := purgeLogger()
	// The App's step proves consent with the approval tasks, so it runs first.
	if policyErr == nil {
		step.run(ctx, policy, logf)
	}
	cleanupErr := purgeLeftovers(ctx, d, pkg, leftovers, logf)
	portalErr := purgePortalEntry(ctx, d, pkg, state, runtime, leftovers.Entry, logf)
	if cleanupErr != nil || portalErr != nil || ctx.Err() != nil {
		return errors.New("purge cleanup pending; protected state retained")
	}
	inv, err := readTaskInventory(runtime)
	if err != nil {
		if inv, err = pinnedInventory(ctx, pkg, runtime); err != nil {
			logf("task cleanup skipped: protected inventory unavailable: package=" + pkg)
			return finishControl(state, runtime, socket, true)
		}
		logf("task cleanup: inventory rebuilt from the approved policy: package=" + pkg)
	}
	return purgeWithInventory(ctx, d, pkg, inv, state, runtime, socket, logf)
}

func controlStop(pkg string, purge bool, runtime string) error {
	state := filepath.Join(stateBase, pkg)
	if _, err := os.Lstat(state); os.IsNotExist(err) {
		return finishControl(state, runtime, filepath.Join(socketBase, pkg), purge)
	}
	if err := secureDir(state, 0700); err != nil {
		return err
	}
	dir := filepath.Join(state, "delegations")
	if err := secureDir(dir, 0700); err != nil {
		return err
	}
	pidpath := filepath.Join(dir, "executor.pid")
	f, err := os.OpenFile(pidpath, os.O_RDWR, 0600)
	if os.IsNotExist(err) {
		if purge {
			return purgeProtected(context.Background(), newDSM(""), pkg, state, runtime, filepath.Join(socketBase, pkg), rootPurge{})
		}
		return finishControl(state, runtime, filepath.Join(socketBase, pkg), false)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if lockFile(f) != nil {
		action := "stop"
		if purge {
			action = "purge"
		}
		pid, err := verifiedPID(f, runtime)
		if err != nil {
			return err
		}
		controlSignal := syscall.SIGUSR1
		if action == "purge" {
			controlSignal = syscall.SIGUSR2
		}
		if err = syscall.Kill(pid, controlSignal); err != nil {
			return err
		}
		if !purge {
			return nil
		}
		// The monitor's purge may first run the App's root step.
		limits := protectedBudgets(pkg, runtime)
		deadline := time.Now().Add(limits.Duration("stop") + limits.Duration("purge"))
		for lockFile(f) != nil {
			if time.Now().After(deadline) {
				return errors.New("purge timeout")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if purge {
		return purgeProtected(context.Background(), newDSM(""), pkg, state, runtime, filepath.Join(socketBase, pkg), rootPurge{})
	}
	return finishControl(state, runtime, filepath.Join(socketBase, pkg), purge)
}

// Only root setup/control may replace the approved monitor. Package stop never
// reaches this signal path; it only pauses via the mailbox.
func verifiedPID(f *os.File, runtime string) (int, error) {
	b, err := readLimited(f, 32)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return 0, errors.New("pid")
	}
	exe := fmt.Sprintf("/proc/%d/exe", pid)
	link, err := os.Readlink(exe)
	if err != nil {
		return 0, errors.New("pid executable mismatch")
	}
	running, err := os.Stat(exe)
	if err != nil || !approvedExecutable(link, running, runtime, rootOwnedReadOnly) {
		return 0, errors.New("pid executable mismatch")
	}
	return pid, nil
}
func stopApprovedProcess(f *os.File, runtime string) error {
	if lockFile(f) == nil {
		return nil
	}
	pid, err := verifiedPID(f, runtime)
	if err != nil {
		return err
	}
	if err = syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(protectedBudgets("", runtime).Duration("stop"))
	for lockFile(f) != nil {
		if time.Now().After(deadline) {
			return errors.New("replace timeout")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}
func finishControl(state, runtime, socket string, purge bool) error {
	if purge && runtime != filepath.Join(state, "runtime") {
		return errors.New("runtime outside package protected state")
	}
	if purge {
		if err := purgeRuntime(state, runtime); err != nil {
			return err
		}
	}
	return os.RemoveAll(socket)
}
func purgeRuntime(state, runtime string) error {
	if runtime != filepath.Join(state, "runtime") {
		return errors.New("runtime outside package protected state")
	}
	return os.RemoveAll(state)
}
func readLimited(f *os.File, n int) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(f, int64(n)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > n {
		return nil, errors.New("size")
	}
	return b, nil
}

func protectedBudgets(pkg, runtime string) budget.Policy {
	if pkg == "" {
		pkg = filepath.Base(filepath.Dir(runtime))
	}
	if p, err := runtimePolicy(pkg, runtime); err == nil {
		return p.budgets()
	}
	return budget.Default()
}
