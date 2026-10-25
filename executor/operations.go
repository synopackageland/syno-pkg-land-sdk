package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Copy performs bulk work outside the commit lock. The callback is the sole
// authority to publish, after a durable intent and one final fence check.
type MoveWriter interface {
	Move(context.Context, Subject, Operation, Delegation, func(func() (MoveReceipt, error)) (MoveReceipt, error)) (MoveReceipt, error)
}
type operationRuntime struct {
	ctx          context.Context
	cancel       context.CancelFunc
	wake         chan struct{}
	done         chan struct{}
	cancelJob    context.CancelFunc
	job          string
	failed       bool
	cancelReason string // commitMu protects the first acknowledged cancellation cause.
}

const moveValidationTimeout = 30 * time.Second

const userCancelReason = "operation-canceled-by-user"

// moveRevalidationInterval spaces the revalidations of a copy in flight.
var moveRevalidationInterval = 5 * time.Second

func (r *operationRuntime) cancelJobLocked(reason string) {
	if r.cancelJob != nil {
		if r.cancelReason == "" {
			r.cancelReason = reason
		}
		r.cancelJob()
	}
}

func isExtension(op string) bool {
	return contains([]string{"files.moveFromStaging", "files.operationStatus", "files.cancelOperation", "delegate.disable", "delegate.enable", "delegate.audit"}, op)
}
func (e *Executor) startOperations(parent context.Context) error {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	if e.async != nil {
		return errors.New("operations already started")
	}
	ops, err := e.Store.Operations()
	if err != nil {
		return err
	}
	for _, op := range ops {
		if op.Package != e.Policy.Package {
			return errors.New("operation package")
		}
		if op.State == "copying" {
			op.State, op.Code = "interrupted", "interrupted"
			if op.PublishIntent {
				op.State, op.Code = "uncertain", "uncertain"
			} else if op.CancelRequested != "" {
				op.State, op.Code, op.Reason = "canceled", "OPERATION_CANCELED", op.CancelRequested
			}
			op.Updated = e.Now().UTC()
			if err = e.Store.PutOperation(op); err != nil {
				return err
			}
		}
	}
	ctx, cancel := context.WithCancel(withClientAddress(parent, localAddress))
	r := &operationRuntime{ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	e.async = r
	go e.operationLoop(r)
	e.wakeLocked()
	return nil
}
func (e *Executor) stopOperations() {
	e.commitMu.Lock()
	r := e.async
	if r != nil {
		r.cancel()
	}
	e.commitMu.Unlock()
	if r != nil {
		<-r.done
		e.commitMu.Lock()
		e.async = nil
		e.commitMu.Unlock()
	}
}
func (e *Executor) wakeLocked() {
	if e.async != nil {
		select {
		case e.async.wake <- struct{}{}:
		default:
		}
	}
}
func (e *Executor) cancelGrantLocked(id, reason string) {
	if e.async == nil {
		return
	}
	ops, err := e.Store.Operations()
	if err != nil {
		e.async.failed = true
		e.async.cancelJobLocked("storage-error")
		return
	}
	for _, op := range ops {
		if op.ID == id && op.State == "queued" {
			op.State = "canceled"
			op.Code = "OPERATION_CANCELED"
			op.Reason = reason
			op.Updated = e.Now().UTC()
			if e.Store.PutOperation(op) != nil {
				e.async.failed = true
			}
		}
		// A copy in flight records the revocation before its writer is signaled,
		// so a stop or crash settles it canceled with this reason, not interrupted.
		if op.ID == id && op.State == "copying" && op.CancelRequested == "" {
			op.CancelRequested = reason
			op.Updated = e.Now().UTC()
			if e.Store.PutOperation(op) != nil {
				e.async.failed = true
			}
		}
		if op.ID == id && op.Job == e.async.job && e.async.cancelJob != nil {
			e.async.cancelJobLocked(reason)
		}
	}
}
func (e *Executor) handleExtension(ctx context.Context, r Request) Response {
	if ctx.Err() != nil {
		return deny("EXECUTOR_PAUSED")
	}
	if r.Version != 1 {
		return deny("INVALID_REQUEST")
	}
	if !contains(e.Policy.Operations, r.Op) {
		return deny("POLICY_DENIED")
	}
	var p Params
	if len(r.Params) == 0 {
		r.Params = json.RawMessage(`{}`)
	}
	if strictJSON(r.Params, &p) != nil {
		return deny("INVALID_REQUEST")
	}
	allowed := map[string]string{"files.moveFromStaging": "delegation_id job_id staging name size conflict", "files.operationStatus": "delegation_id job_id", "files.cancelOperation": "delegation_id job_id", "delegate.disable": "", "delegate.enable": "", "delegate.audit": ""}
	var keys map[string]json.RawMessage
	if json.Unmarshal(r.Params, &keys) != nil {
		return deny("INVALID_REQUEST")
	}
	for k := range keys {
		if !contains(strings.Fields(allowed[r.Op]), k) {
			return deny("INVALID_REQUEST")
		}
	}
	if r.Op == "delegate.disable" || r.Op == "delegate.enable" || r.Op == "delegate.audit" {
		return e.administration(ctx, r)
	}
	if !tokenRE.MatchString(p.Job) || !idRE.MatchString(p.ID) {
		return deny("INVALID_REQUEST")
	}
	if r.Op != "files.moveFromStaging" {
		// Authenticated management is owner-bound. Auth-less producer lookup/cancel
		// requires BOTH protected grant reference and original job, never a new job.
		var caller Subject
		hasAuth := r.Auth.present()
		if hasAuth {
			var code string
			caller, code = e.verified(ctx, r.Auth)
			if code != "" {
				return deny(code)
			}
		}
		e.commitMu.Lock()
		defer e.commitMu.Unlock()
		op, err := e.Store.GetOperation(p.Job)
		if os.IsNotExist(err) && r.Op == "files.cancelOperation" {
			return e.cancelUnadmittedLocked(p, hasAuth, caller)
		}
		if err != nil || op.Package != e.Policy.Package || op.ID != p.ID {
			return deny("operation-not-found")
		}
		if hasAuth && (caller.Username != op.Subject.Username || caller.UID != op.Subject.UID) {
			return deny("operation-not-found")
		}
		if r.Op == "files.cancelOperation" {
			if code := e.cancelOperationLocked(&op); code != "" {
				return deny(code)
			}
		}
		return success(publicOperation(op))
	}
	_, hasConflict := keys["conflict"]
	required := len(keys)
	if hasConflict {
		required--
	}
	if required != 5 || !validRelative(p.Staging) || (hasConflict && !validConflict(p.Conflict)) {
		return deny("INVALID_REQUEST")
	}
	conflict := storedConflict(p.Conflict)
	if code := destinationNameCode(p.Name); code != "" {
		return deny(code)
	}
	if _, ok := keys["size"]; !ok || p.Size < 0 || p.Size > maxMoveBytes || string(keys["size"]) == "null" {
		return deny("invalid-size")
	}
	if _, ok := e.Writer.(MoveWriter); !ok {
		return deny("EXECUTOR_PAUSED")
	}
	// Never reinterpret provided credentials as offline authority.
	if r.Auth.present() {
		s, code := e.verified(ctx, r.Auth)
		if code != "" {
			return deny(code)
		}
		d, err := e.Store.Get(p.ID)
		if err != nil || s.UID != d.Subject.UID || s.Username != d.Subject.Username {
			return deny("DELEGATION_DENIED")
		}
	}
	e.commitMu.Lock()
	old, err := e.Store.GetOperation(p.Job)
	e.commitMu.Unlock()
	if err == nil {
		if old.Package == e.Policy.Package && old.ID == p.ID && old.canceledBeforeAdmission() {
			return success(publicOperation(old))
		}
		if old.Package != e.Policy.Package || old.ID != p.ID || old.Staging != p.Staging || old.Name != p.Name || old.Size != p.Size || old.Conflict != conflict {
			return deny("operation-conflict")
		}
		return success(publicOperation(old))
	}
	if !os.IsNotExist(err) {
		return deny("STORAGE_ERROR")
	}
	e.commitMu.Lock()
	disabled, disabledErr := e.Store.Disabled()
	e.commitMu.Unlock()
	if disabledErr != nil {
		return deny("STORAGE_ERROR")
	}
	if disabled {
		return deny("disabled")
	}
	d, err := e.Store.Get(p.ID)
	if err != nil {
		return deny("DELEGATION_DENIED")
	}
	ctx = withGrantAddress(ctx, r.Auth, d.ClientAddr)
	addr, _ := clientAddress(ctx)
	s, code := e.validateMove(ctx, d)
	if code != "" {
		if revokesGrant(code) && e.invalidateMove(d) != nil {
			return deny("STORAGE_ERROR")
		}
		return deny(code)
	}
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	if e.async == nil || e.async.ctx.Err() != nil || e.async.failed {
		return deny("EXECUTOR_PAUSED")
	}
	if code = e.moveFenceLocked(d); code != "" {
		return deny(code)
	}
	if old, err = e.Store.GetOperation(p.Job); err == nil {
		if old.ID == p.ID && old.canceledBeforeAdmission() {
			return success(publicOperation(old))
		}
		if old.ID != p.ID || old.Staging != p.Staging || old.Name != p.Name || old.Size != p.Size || old.Conflict != conflict {
			return deny("operation-conflict")
		}
		return success(publicOperation(old))
	} else if !os.IsNotExist(err) {
		return deny("STORAGE_ERROR")
	}
	ops, err := e.Store.Operations()
	if err != nil {
		return deny("STORAGE_ERROR")
	}
	queued := 0
	for _, op := range ops {
		if op.State == "queued" {
			queued++
		}
	}
	if queued >= maxQueued {
		return deny("queue-full")
	}
	op := Operation{Version: 1, Package: e.Policy.Package, ID: d.ID, Job: p.Job, Subject: s, Staging: p.Staging, Name: p.Name, Size: p.Size, Conflict: conflict, State: "queued", Updated: e.Now().UTC(), ClientAddr: addr}
	if e.auditLocked(s, r.Op, op.Job, d.Path, "admitting") != nil || e.Store.PutOperation(op) != nil {
		return deny("STORAGE_ERROR")
	}
	e.wakeLocked()
	return success(publicOperation(op))
}
func publicOperation(op Operation) any {
	out := map[string]any{"job_id": op.Job, "state": op.State}
	if op.State == "committed" {
		out["receipt"] = map[string]any{"path": op.Path, "size": op.Size}
	}
	if op.State == "copying" && op.CancelRequested != "" {
		op.Code, op.Reason = "CANCEL_REQUESTED", op.CancelRequested
	}
	if op.Code != "" {
		out["code"] = op.Code
	}
	if op.Reason != "" {
		out["reason"] = op.Reason
	}
	return out
}

// cancelOperationLocked cancels a queued operation at once. A copy in flight
// keeps copying until its writer stops, so the request is made durable first:
// a restart settles it canceled instead of interrupted. Terminal operations and
// repeated requests are answered unchanged.
func (e *Executor) cancelOperationLocked(op *Operation) string {
	switch {
	case op.State == "queued":
		op.State, op.Code, op.Reason = "canceled", "OPERATION_CANCELED", userCancelReason
	case op.State == "copying" && op.CancelRequested == "":
		op.CancelRequested = userCancelReason
	default:
		return ""
	}
	op.Updated = e.Now().UTC()
	if e.Store.PutOperation(*op) != nil {
		return "STORAGE_ERROR"
	}
	if op.State == "copying" && e.async != nil && e.async.job == op.Job {
		e.async.cancelJobLocked(userCancelReason)
	}
	if e.auditLocked(op.Subject, "files.cancelOperation", op.Job, "", "canceled") != nil {
		return "STORAGE_ERROR"
	}
	return ""
}

// cancelUnadmittedLocked answers a cancel that arrives before its admission,
// for example while a lost or slow admission is still being validated. A
// tombstone under the package grant makes that admission return canceled.
func (e *Executor) cancelUnadmittedLocked(p Params, hasAuth bool, caller Subject) Response {
	d, err := e.Store.Get(p.ID)
	if err != nil || d.Package != e.Policy.Package || (hasAuth && (caller.Username != d.Subject.Username || caller.UID != d.Subject.UID)) {
		return deny("operation-not-found")
	}
	op := Operation{Version: 1, Package: e.Policy.Package, ID: d.ID, Job: p.Job, Subject: d.Subject, State: "canceled", Code: "OPERATION_CANCELED", Reason: userCancelReason, Updated: e.Now().UTC()}
	if e.Store.PutOperation(op) != nil || e.auditLocked(op.Subject, "files.cancelOperation", op.Job, "", "canceled") != nil {
		return deny("STORAGE_ERROR")
	}
	return success(publicOperation(op))
}

func (e *Executor) administration(ctx context.Context, r Request) Response {
	s, code := e.verified(ctx, r.Auth)
	if code != "" {
		return deny(code)
	}
	if r.Op != "delegate.audit" && !s.Admin {
		return deny("ADMIN_REQUIRED")
	}
	if err := e.DSM.Allow(ctx, s, e.Policy.App, e.Policy.Entry); err != nil {
		return deny(dsmRefusal(err, "APP_DENIED"))
	}
	e.commitMu.Lock()
	locked := true
	defer func() {
		if locked {
			e.commitMu.Unlock()
		}
	}()
	if r.Op == "delegate.audit" {
		out := []AuditEvent{}
		total := 0
		sizes := []int{}
		for _, name := range []string{"audit.previous", "audit.jsonl"} {
			f, err := os.Open(filepath.Join(e.Store.Dir, name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return deny("STORAGE_ERROR")
			}
			scan := bufio.NewScanner(f)
			scan.Buffer(make([]byte, 4096), maxRequest)
			for scan.Scan() {
				var event AuditEvent
				if strictJSON(scan.Bytes(), &event) != nil {
					f.Close()
					return deny("STORAGE_ERROR")
				}
				if event.Package == e.Policy.Package && (s.Admin || (s.Username == event.Username && s.UID == event.UID)) {
					out = append(out, event)
					n := len(scan.Bytes()) + 1
					total += n
					sizes = append(sizes, n)
					for len(out) > 256 || total > maxOutput-4096 {
						total -= sizes[0]
						sizes = sizes[1:]
						out = out[1:]
					}
				}
			}
			err = scan.Err()
			f.Close()
			if err != nil {
				return deny("STORAGE_ERROR")
			}
		}
		return success(map[string]any{"events": out})
	}
	wasDisabled, disabledErr := e.Store.Disabled()
	if disabledErr != nil {
		return deny("STORAGE_ERROR")
	}
	if r.Op == "delegate.enable" && !wasDisabled {
		return success(map[string]any{"disabled": false})
	}
	// Disable becomes durable FIRST, including when revocation subsequently fails.
	if r.Op == "delegate.disable" {
		if e.Store.SetDisabled(true) != nil {
			return deny("STORAGE_ERROR")
		}
	}
	ds, err := e.Store.List()
	if err != nil {
		return deny("STORAGE_ERROR")
	}
	var revoked []string
	for _, d := range ds {
		if d.Package != e.Policy.Package {
			continue
		}
		d.Revoked = true
		if e.Store.Put(d) != nil {
			return deny("STORAGE_ERROR")
		}
		e.cancelGrantLocked(d.ID, "integration-disabled")
		revoked = append(revoked, d.ID)
	}
	if e.auditLocked(s, r.Op, "", "", r.Op) != nil {
		return deny("STORAGE_ERROR")
	}
	if r.Op == "delegate.enable" {
		if e.Store.SetDisabled(false) != nil {
			return deny("STORAGE_ERROR")
		}
	}
	pending := e.legacyPendingLocked("")
	e.commitMu.Unlock()
	locked = false
	if waitLegacy(ctx, pending) != nil {
		return deny("EXECUTOR_PAUSED")
	}
	// As delegate.revoke does, stop File Station tasks the revoked grants started.
	for _, id := range revoked {
		e.stopTasks(id)
	}
	return success(map[string]any{"disabled": r.Op == "delegate.disable"})
}
func (e *Executor) moveFenceLocked(d Delegation) string {
	disabled, err := e.Store.Disabled()
	if err != nil {
		return "STORAGE_ERROR"
	}
	if disabled {
		return "disabled"
	}
	fresh, err := e.Store.Get(d.ID)
	if err != nil || fresh.Package != e.Policy.Package || fresh.Revoked || !e.scopeMatchesLocked(fresh) || fresh.Lifetime != "until-revoked" || fresh.Root != d.Root || fresh.Directory != d.Directory {
		return "DELEGATION_DENIED"
	}
	return ""
}

// moveCancelReason names why a running move stopped, using only spec reasons.
func moveCancelReason(fence string) string {
	switch fence {
	case "STORAGE_ERROR":
		return "storage-error"
	case "disabled":
		return "integration-disabled"
	case "DELEGATION_DENIED":
		return "grant-revoked"
	}
	return "validation-denied"
}
func (e *Executor) validateMove(ctx context.Context, d Delegation) (Subject, string) {
	if ctx.Err() != nil {
		return Subject{}, "EXECUTOR_PAUSED"
	}
	if d.Package != e.Policy.Package || d.Revoked || d.Lifetime != "until-revoked" || e.Policy.Lifetime != d.Lifetime || !e.scopeMatches(d) || !contains(e.Policy.Destinations, d.DestinationKey) || !contains(d.Ops, "files.moveFromStaging") {
		return Subject{}, "DELEGATION_DENIED"
	}
	s, code := e.eligible(ctx, d.Subject.Username, d.Subject.UID, true)
	if ctx.Err() != nil {
		return s, "EXECUTOR_PAUSED"
	}
	if code != "" {
		return s, code
	}
	moveValidationStage(ctx, "scope-root")
	root, err := e.DSM.ScopeRoot(ctx, s, d.Scope)
	if moveValidationDeadline(ctx, err) {
		return s, "EXECUTOR_PAUSED"
	}
	if ctx.Err() != nil {
		return s, "EXECUTOR_PAUSED"
	}
	if err != nil {
		return s, "SCOPE_DENIED"
	}
	moveValidationStage(ctx, "directory-identity")
	check := d
	check.Root = root
	check.Path = scopeVirtual(d.Scope)
	real, err := mappedDirectory(check, d.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, "not-found"
	}
	if err != nil || real != d.Root {
		return s, "SCOPE_DENIED"
	}
	di, err := bindDirectory(real)
	if err != nil || di != d.Directory {
		return s, "SCOPE_DENIED"
	}
	moveValidationStage(ctx, "permission")
	if _, err = e.DSM.API(ctx, s.Username, "files.checkPermission", map[string]any{"path": d.Path}); err != nil {
		if ctx.Err() != nil || moveValidationDeadline(ctx, err) {
			return s, "EXECUTOR_PAUSED"
		}
		return s, "PERMISSION_DENIED"
	}
	if ctx.Err() != nil {
		return s, "EXECUTOR_PAUSED"
	}
	return s, ""
}
func (e *Executor) operationLoop(r *operationRuntime) {
	defer close(r.done)
	e.recoverParts(r)
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.wake:
		}
		for {
			e.commitMu.Lock()
			ops, err := e.Store.Operations()
			if err != nil {
				r.failed = true
				e.commitMu.Unlock()
				return
			}
			var op Operation
			found := false
			for _, item := range ops {
				if item.State == "queued" {
					op = item
					found = true
					break
				}
			}
			if !found || r.ctx.Err() != nil || r.failed {
				e.commitMu.Unlock()
				break
			}
			ctx, cancel := context.WithTimeout(r.ctx, 24*time.Hour)
			r.job = op.Job
			r.cancelReason = ""
			r.cancelJob = cancel
			op.State = "copying"
			op.Updated = e.Now().UTC()
			if err = e.Store.PutOperation(op); err != nil {
				r.failed = true
				cancel()
				e.commitMu.Unlock()
				return
			}
			e.commitMu.Unlock()
			e.performMove(ctx, r, op)
			cancel()
			e.commitMu.Lock()
			r.job = ""
			r.cancelJob = nil
			e.commitMu.Unlock()
		}
	}
}
func (e *Executor) performMove(ctx context.Context, r *operationRuntime, op Operation) {
	ctx = withGrantAddress(ctx, Auth{}, op.ClientAddr)
	release, slotErr := e.acquireCopy(ctx)
	if slotErr == nil {
		defer release()
	}
	d, err := e.Store.Get(op.ID)
	var s Subject
	code := "DELEGATION_DENIED"
	if err == nil {
		s, code = e.validateMove(ctx, d)
	}
	if slotErr != nil {
		code = "EXECUTOR_PAUSED"
	}
	var receipt MoveReceipt
	if code == "" {
		// A deadline is an unavailable observation, not evidence of revocation.
		// Bulk bytes remain private; publication still requires fresh validation.
		// Local revoke/scope/disable handlers cancel directly under commitMu, even
		// while this bounded DSM check is blocked by load or swap.
		pollCtx, stop := context.WithCancel(ctx)
		var poll sync.WaitGroup
		poll.Add(1)
		go func() {
			defer poll.Done()
			diagnostics := 8
			tick := time.NewTicker(moveRevalidationInterval)
			defer tick.Stop()
			for {
				select {
				case <-pollCtx.Done():
					return
				case <-tick.C:
					checkCtx, checkCancel := context.WithTimeout(pollCtx, moveValidationTimeout)
					current, c, transient := e.observeMoveValidation(checkCtx, d, op.Job, &diagnostics)
					transient = transient && pollCtx.Err() == nil
					if c == "" && !sameIdentity(s, current) {
						c = "IDENTITY_CHANGED"
					}
					checkCancel()
					e.commitMu.Lock()
					fence := e.moveFenceLocked(d)
					if revokesGrant(c) && pollCtx.Err() == nil {
						fresh, x := e.Store.Get(d.ID)
						if x == nil {
							fresh.Revoked = true
							if e.Store.Put(fresh) != nil {
								r.failed = true
							}
						}
					}
					if fence != "" || (c != "" && !transient && pollCtx.Err() == nil) {
						r.cancelJobLocked(moveCancelReason(fence))
					}
					e.commitMu.Unlock()
				}
			}
		}()
		writerCtx := context.WithValue(ctx, partRecordKey{}, func(part partMetadata) error {
			e.commitMu.Lock()
			defer e.commitMu.Unlock()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if code := e.moveFenceLocked(d); code != "" {
				return errors.New(code)
			}
			op.Part = &part
			if err := e.Store.PutOperation(op); err != nil {
				r.failed = true
				return err
			}
			return nil
		})
		receipt, err = e.Writer.(MoveWriter).Move(writerCtx, s, op, d, func(publish func() (MoveReceipt, error)) (MoveReceipt, error) {
			current, c := e.validateMove(ctx, d)
			if c != "" {
				code = c
				if revokesGrant(c) && e.invalidateMove(d) != nil {
					return MoveReceipt{}, errors.New("STORAGE_ERROR")
				}
				return MoveReceipt{}, errors.New(c)
			}
			// Group changes between opening the child and publishing require a new attempt.
			if !sameIdentity(s, current) {
				return MoveReceipt{}, errors.New("IDENTITY_CHANGED")
			}
			e.commitMu.Lock()
			defer e.commitMu.Unlock()
			if ctx.Err() != nil || r.ctx.Err() != nil {
				return MoveReceipt{}, ctx.Err()
			}
			if c = e.moveFenceLocked(d); c != "" {
				return MoveReceipt{}, errors.New(c)
			}
			op.PublishIntent = true
			op.Updated = e.Now().UTC()
			if e.Store.PutOperation(op) != nil {
				r.failed = true
				return MoveReceipt{}, errors.New("STORAGE_ERROR")
			}
			out, x := publish()
			if x == nil {
				op.State = "committed"
				op.Path = strings.TrimSuffix(d.Path, "/") + "/" + receiptName(op, out)
				if out.CleanupPending {
					op.Code = "CLEANUP_PENDING"
				}
				op.Updated = e.Now().UTC()
				if e.Store.PutOperation(op) != nil {
					r.failed = true
					return out, errors.New("STORAGE_ERROR")
				}
			}
			return out, x
		})
		stop()
		poll.Wait()
	}
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	saved, x := e.Store.GetOperation(op.Job)
	if x != nil {
		r.failed = true
		return
	}
	if saved.State == "committed" {
		if receipt.CleanupPending {
			saved.Code = "CLEANUP_PENDING"
		}
		if e.Store.PutOperation(saved) != nil {
			r.failed = true
		}
		if e.auditLocked(op.Subject, "files.moveFromStaging", op.Job, saved.Path, "committed") != nil {
			r.failed = true
		}
		return
	}
	saved.State = "interrupted"
	saved.Code = code
	var rejected publishRejectedError
	if errors.As(err, &rejected) {
		saved.State = "interrupted"
		saved.Code = rejected.Code
	} else if saved.PublishIntent {
		saved.State = "uncertain"
		saved.Code = "uncertain"
	} else if saved.CancelRequested != "" {
		saved.State = "canceled"
		saved.Code = "OPERATION_CANCELED"
		saved.Reason = saved.CancelRequested
	} else if ctx.Err() != nil && r.ctx.Err() == nil {
		saved.State = "canceled"
		saved.Code = "OPERATION_CANCELED"
		saved.Reason = r.cancelReason
		if saved.Reason == "" {
			saved.Reason = "operation-deadline-exceeded"
		}
	} else if r.ctx.Err() != nil && code == "" {
		// The executor stopped the writer; nothing about the destination failed.
		saved.Code = "EXECUTOR_PAUSED"
	} else if err != nil {
		if code == "" {
			saved.Code = "WRITE_FAILED"
		}
	}
	// Eligibility failure invalidates the grant; transient copy errors do not.
	if revokesGrant(code) {
		fresh, x := e.Store.Get(op.ID)
		if x == nil {
			fresh.Revoked = true
			if e.Store.Put(fresh) != nil {
				r.failed = true
			}
		}
	}
	saved.Updated = e.Now().UTC()
	if e.Store.PutOperation(saved) != nil || e.auditLocked(op.Subject, "files.moveFromStaging", op.Job, d.Path, saved.State) != nil {
		r.failed = true
	}
}
func sameIdentity(a, b Subject) bool {
	return a.Username == b.Username && a.UID == b.UID && a.GID == b.GID && sameGroups(a.Groups, b.Groups)
}

// Legacy runner API calls use a short admission lease. Revoke/disable persist
// their fence, then drain these calls OUTSIDE the commit lock before replying.
func (e *Executor) beginLegacy(d Delegation) (func(), string) {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	disabled, err := e.Store.Disabled()
	fresh, x := e.Store.Get(d.ID)
	if err != nil || x != nil {
		return nil, "STORAGE_ERROR"
	}
	if disabled {
		return nil, "disabled"
	}
	if fresh.Revoked || (!(isWebGrant(fresh) && fresh.Lifetime == "until-revoked") && !e.Now().Before(fresh.Expires)) {
		return nil, "DELEGATION_DENIED"
	}
	if e.legacyCalls == nil {
		e.legacyCalls = map[string][]chan struct{}{}
	}
	done := make(chan struct{})
	e.legacyCalls[d.ID] = append(e.legacyCalls[d.ID], done)
	return func() {
		e.commitMu.Lock()
		defer e.commitMu.Unlock()
		close(done)
		calls := e.legacyCalls[d.ID]
		for i, ch := range calls {
			if ch == done {
				e.legacyCalls[d.ID] = append(calls[:i], calls[i+1:]...)
				break
			}
		}
		if len(e.legacyCalls[d.ID]) == 0 {
			delete(e.legacyCalls, d.ID)
		}
	}, ""
}
func (e *Executor) legacyPendingLocked(id string) []chan struct{} {
	out := []chan struct{}{}
	for key, calls := range e.legacyCalls {
		if id == "" || key == id {
			out = append(out, calls...)
		}
	}
	return out
}
func waitLegacy(ctx context.Context, pending []chan struct{}) error {
	for _, done := range pending {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
	return nil
}

// revokesGrant reports whether a failed revalidation ends the grant. A denial
// the user or an administrator can undo, such as an ACL or an Application
// Privileges rule, fails only this operation: every later operation is
// validated again anyway. A destination folder moved away is not-found until
// it is back; a different folder at the same path is SCOPE_DENIED and does
// revoke.
func revokesGrant(code string) bool {
	return code != "" && code != "EXECUTOR_PAUSED" && code != "PERMISSION_DENIED" && code != "not-found" && code != "APP_DENIED"
}
func (e *Executor) invalidateMove(d Delegation) error {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	fresh, err := e.Store.Get(d.ID)
	if err != nil {
		return err
	}
	fresh.Revoked = true
	if err = e.Store.Put(fresh); err != nil {
		return err
	}
	e.cancelGrantLocked(d.ID, "validation-denied")
	return e.auditLocked(d.Subject, "delegate.invalidate", "", d.Path, "revoked")
}

type PartCleaner interface {
	CleanupRecorded(context.Context, Subject, partMetadata) error
}

// PartPublisher reports whether an operation's recorded part inode is already
// the final name, so a crash after publish intent settles without a new copy.
type PartPublisher interface {
	PublishedPart(context.Context, Operation) (string, bool)
}

func (e *Executor) settleUncertain(r *operationRuntime, op Operation) {
	checker, ok := e.Writer.(PartPublisher)
	if !ok || op.State != "uncertain" || op.Part == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	name, published := checker.PublishedPart(ctx, op)
	cancel()
	if !published {
		return
	}
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	fresh, err := e.Store.GetOperation(op.Job)
	d, x := e.Store.Get(op.ID)
	if err != nil || x != nil || fresh.State != "uncertain" {
		return
	}
	fresh.State, fresh.Code = "committed", ""
	fresh.Path = strings.TrimSuffix(d.Path, "/") + "/" + name
	fresh.Updated = e.Now().UTC()
	if e.Store.PutOperation(fresh) != nil || e.auditLocked(fresh.Subject, "files.moveFromStaging", fresh.Job, fresh.Path, "committed") != nil {
		r.failed = true
	}
}

func (e *Executor) recoverParts(r *operationRuntime) {
	cleaner, ok := e.Writer.(PartCleaner)
	if !ok {
		return
	}
	e.commitMu.Lock()
	ops, err := e.Store.Operations()
	e.commitMu.Unlock()
	if err != nil {
		e.commitMu.Lock()
		r.failed = true
		e.commitMu.Unlock()
		return
	}
	for _, op := range ops {
		if op.Part == nil || op.State == "queued" || op.State == "copying" {
			continue
		}
		e.settleUncertain(r, op)
		s, code := e.eligible(withGrantAddress(r.ctx, Auth{}, op.ClientAddr), op.Subject.Username, op.Subject.UID, true)
		cleanupErr := errors.New("cleanup ineligible")
		if code == "" {
			ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
			cleanupErr = cleaner.CleanupRecorded(ctx, s, *op.Part)
			cancel()
		}
		e.commitMu.Lock()
		fresh, x := e.Store.GetOperation(op.Job)
		if x == nil {
			if cleanupErr == nil {
				fresh.Part = nil
			} else if fresh.State == "committed" {
				fresh.Code = "CLEANUP_PENDING"
			}
			if e.Store.PutOperation(fresh) != nil {
				r.failed = true
			}
		} else {
			r.failed = true
		}
		e.commitMu.Unlock()
		if r.ctx.Err() != nil {
			return
		}
	}
}

// Legacy and permanent writers share one package-wide, cancelable bulk slot.
// Waiting for a large new copy must not hold a lock or outlive a legacy request.
func (e *Executor) acquireCopy(ctx context.Context) (func(), error) {
	e.commitMu.Lock()
	if e.copySlot == nil {
		e.copySlot = make(chan struct{}, 1)
	}
	slot := e.copySlot
	e.commitMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	}
}
