package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Bounds of root.run: its request may carry a mebibyte of params, its reply
// stays within the 64 KiB a client reads, anything larger is staged.
const (
	maxRootRequest   = 1 << 20
	maxRootReply     = 64 << 10
	maxRootStderr    = 16 << 10
	maxRootStaging   = 256 << 20
	rootStagingAge   = time.Hour
	rootRunSlots     = 2
	rootRunRequestBy = maxRootRunTimeout + 10*time.Second
	// executorOpBound is the default answer bound; raw WebAPI has its own.
	executorOpBound = 30 * time.Second
)

// rootCall is one start of a pinned root program. Env holds only the
// executor's SYNOPKGLAND_* variables; the runner adds the fixed base.
type rootCall struct {
	Name           string
	Entry          RootEntry
	Mode           string   // run, job, purge or stop
	Args           []string // a lifecycle step's argument
	Stdin          []byte
	Env            []string
	Stdout, Stderr io.Writer
	// Files are a job's control and progress pipes, fds 4 and 5.
	Files []*os.File
}

// RootRunner starts pinned root programs in their own process group.
type RootRunner interface {
	Start(rootCall) (rootProcess, error)
}

type rootProcess interface {
	// Wait is the exit code; an error means the program ended by a signal
	// or could not be waited for.
	Wait() (int, error)
	// Kill sends SIGKILL to the program's process group.
	Kill()
	// Group is the process group, 0 when there is none.
	Group() int
}

// RootStaging is the package staging folder that receives large root output
// as root-<random> files the package account owns.
type RootStaging interface {
	Create() (*os.File, string, error)
	Remove(name string)
	// Used is the size of the root-* files there, after removing those older
	// than an hour that no client consumed.
	Used() (int64, error)
}

var (
	errRootTimeout     = errors.New("root program timeout")
	errOutputLimit     = errors.New("root output limit")
	errRootStagingFull = errors.New("root staging full")
	errRootStaging     = errors.New("root output staging unavailable")
)

// runRoot starts c and waits at most timeout; ctx ending or the deadline
// kill the program's process group.
func runRoot(ctx context.Context, runner RootRunner, c rootCall, timeout time.Duration, abort <-chan struct{}) (int, error) {
	p, err := runner.Start(c)
	if err != nil {
		return -1, err
	}
	type exit struct {
		code int
		err  error
	}
	done := make(chan exit, 1)
	go func() { code, err := p.Wait(); done <- exit{code, err} }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case x := <-done:
		return x.code, x.err
	case <-timer.C:
		p.Kill()
		<-done
		return -1, errRootTimeout
	case <-abort:
		p.Kill()
		x := <-done
		return x.code, x.err
	case <-ctx.Done():
		p.Kill()
		<-done
		return -1, ctx.Err()
	}
}

func randomID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// rootEnv is a program's executor-provided environment.
func (e *Executor) rootEnv(mode string, extra ...string) []string {
	return append(stepEnv(e.Policy.Package, mode, e.RootState), extra...)
}

// stepEnv is the environment every root program gets.
func stepEnv(pkg, mode, state string) []string {
	env := []string{"SYNOPKGLAND_PACKAGE=" + pkg, "SYNOPKGLAND_ROOT_MODE=" + mode}
	if state != "" {
		env = append(env, "SYNOPKGLAND_ROOT_STATE="+state)
	}
	return env
}

// rootEntry is the policy entry a session may use; lifecycle steps only run
// from the lifecycle.
func (e *Executor) rootEntry(name string) (RootEntry, string) {
	if !tokenRE.MatchString(name) {
		return RootEntry{}, "INVALID_REQUEST"
	}
	entry, ok := e.Policy.Root[name]
	if !ok || name == e.Policy.Purge || name == e.Policy.Stop || name == e.Policy.Start {
		return RootEntry{}, "POLICY_DENIED"
	}
	return entry, ""
}

// rootCaller is the administrator whose session asks for root work and whom
// Application Privileges allow to use the App.
func (e *Executor) rootCaller(ctx context.Context, auth Auth) (Subject, string) {
	s, code := e.verified(ctx, auth)
	if code != "" {
		return s, code
	}
	if !s.Admin {
		return s, "ADMIN_REQUIRED"
	}
	if err := e.DSM.Allow(ctx, s, e.Policy.App, e.Policy.Entry); err != nil {
		return s, dsmRefusal(err, "APP_DENIED")
	}
	return s, ""
}

// rootParams is the params a program reads on stdin, checked against its schema.
func rootParams(entry RootEntry, raw json.RawMessage) ([]byte, bool) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	return raw, entry.Schema.check(raw) == nil
}

func (e *Executor) rootRun(ctx context.Context, auth Auth, p Params) Response {
	entry, code := e.rootEntry(p.Name)
	if code == "" && entry.Run == nil {
		code = "POLICY_DENIED"
	}
	if code != "" {
		return deny(code)
	}
	stdin, ok := rootParams(entry, p.RootParams)
	if !ok {
		return deny("INVALID_REQUEST")
	}
	s, code := e.rootCaller(ctx, auth)
	if code != "" {
		return deny(code)
	}
	if e.Runner == nil || (e.Policy.RootState && e.RootState == "") {
		return deny("ROOT_FAILED")
	}
	if !e.rootSlot() {
		log.Printf("root.run busy: package=%s name=%s running=%d", e.Policy.Package, p.Name, rootRunSlots)
		return deny("ROOT_BUSY")
	}
	defer e.rootSlotDone()
	id, err := randomID()
	if err != nil {
		return deny("INTERNAL")
	}
	out := &rootOutput{limit: entry.Run.maxOutput(), staging: e.RootStaging, failed: make(chan struct{})}
	defer out.discard()
	stderr := &headBuffer{limit: maxRootStderr}
	call := rootCall{Name: p.Name, Entry: entry, Mode: "run", Stdin: stdin, Env: e.rootEnv("run", "SYNOPKGLAND_REQUESTED_BY="+s.Username, "SYNOPKGLAND_REQUEST_ID="+id), Stdout: out, Stderr: stderr}
	exit, err := runRoot(ctx, e.Runner, call, entry.Run.timeout(), out.failed)
	switch {
	case out.err != nil:
		return e.rootOutputFailed(p.Name, out.err)
	case errors.Is(err, errRootTimeout):
		return deny("ROOT_TIMEOUT")
	case ctx.Err() != nil:
		return deny("EXECUTOR_PAUSED")
	case err != nil:
		return deny("ROOT_FAILED")
	}
	reply := map[string]any{"request_id": id, "exit_code": exit, "stderr": strings.ToValidUTF8(stderr.buf.String(), "�")}
	if out.file == nil && utf8.Valid(out.mem.Bytes()) {
		reply["output"] = out.mem.String()
		if b, err := json.Marshal(success(reply)); err == nil && len(b) < maxRootReply {
			return success(reply)
		}
		delete(reply, "output")
	}
	name, size, err := out.keep()
	if err != nil {
		return e.rootOutputFailed(p.Name, err)
	}
	reply["staging"], reply["size"] = name, size
	return success(reply)
}

// rootOutputFailed refuses a run whose output could not be kept; a staging
// folder the executor cannot use is logged and named in the message.
func (e *Executor) rootOutputFailed(name string, err error) Response {
	switch {
	case errors.Is(err, errOutputLimit):
		return deny("limit-exceeded")
	case errors.Is(err, errRootStagingFull):
		return deny("ROOT_STAGING_FULL")
	case errors.Is(err, errRootStaging):
		log.Printf("root.run output staging unavailable: package=%s name=%s: %v", e.Policy.Package, name, err)
		return Response{Code: "ROOT_FAILED", Message: errRootStaging.Error()}
	}
	return deny("ROOT_FAILED")
}

func (e *Executor) rootSlot() bool {
	e.rootMu.Lock()
	defer e.rootMu.Unlock()
	if e.rootRunning >= rootRunSlots {
		return false
	}
	e.rootRunning++
	return true
}

func (e *Executor) rootSlotDone() {
	e.rootMu.Lock()
	e.rootRunning--
	e.rootMu.Unlock()
}

// headBuffer keeps the first limit bytes written and drops the rest.
type headBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *headBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// rootOutput keeps a program's stdout in memory until it outgrows a reply,
// then in a staging file, up to limit bytes. Failed closes on its first
// failure, so the program is killed rather than left blocked on its pipe.
type rootOutput struct {
	limit int64
	// Zero retains root.run's original threshold; raw base64 spills earlier.
	spillAt int
	n       int64
	mem     bytes.Buffer
	staging RootStaging
	// Raw transfers reserve their quota before streaming; root.run keeps its
	// existing 256 MiB quota when this is zero.
	stagingLimit int64
	file         *os.File
	name         string
	failed       chan struct{}
	err          error
}

func (o *rootOutput) Write(b []byte) (int, error) {
	if o.err == nil && o.n+int64(len(b)) > o.limit {
		o.err = errOutputLimit
	}
	threshold := o.spillAt
	if threshold == 0 {
		threshold = maxRootReply
	}
	if o.err == nil && o.file == nil && o.mem.Len()+len(b) >= threshold {
		o.err = o.spill()
	}
	if o.err == nil && o.file != nil {
		_, o.err = o.file.Write(b)
	} else if o.err == nil {
		o.mem.Write(b)
	}
	if o.err != nil {
		select {
		case <-o.failed:
		default:
			close(o.failed)
		}
		return 0, o.err
	}
	o.n += int64(len(b))
	return len(b), nil
}

// spill moves the output into a new staging file, within the staging quota.
func (o *rootOutput) spill() error {
	if o.staging == nil {
		return errRootStaging
	}
	used, err := o.staging.Used()
	if err != nil {
		return fmt.Errorf("%w: %w", errRootStaging, err)
	}
	quota := o.stagingLimit
	if quota == 0 {
		quota = maxRootStaging
	}
	if used+o.limit > quota {
		return errRootStagingFull
	}
	if o.file, o.name, err = o.staging.Create(); err != nil {
		return fmt.Errorf("%w: %w", errRootStaging, err)
	}
	_, err = o.file.Write(o.mem.Bytes())
	o.mem.Reset()
	return err
}

// keep hands the staged output to the client, which removes it.
func (o *rootOutput) keep() (string, int64, error) {
	if o.file == nil {
		if err := o.spill(); err != nil {
			return "", 0, err
		}
	}
	if err := o.file.Sync(); err != nil {
		return "", 0, err
	}
	name := o.name
	o.file.Close()
	o.file, o.name = nil, ""
	return name, o.n, nil
}

func (o *rootOutput) discard() {
	if o.file != nil {
		o.file.Close()
		o.staging.Remove(o.name)
	}
}
