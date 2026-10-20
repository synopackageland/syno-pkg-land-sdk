package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

type slowMoveDSM struct {
	*fakeDSM
	armed   atomic.Bool
	timeout bool
	checked chan struct{}
	entered chan struct{}
	nested  bool
}

func (d *slowMoveDSM) API(ctx context.Context, runner, op string, params map[string]any) (json.RawMessage, error) {
	if op == "files.checkPermission" && d.armed.CompareAndSwap(true, false) {
		if d.entered != nil {
			close(d.entered)
		}
		if d.nested {
			close(d.checked)
			return nil, context.DeadlineExceeded
		}
		if d.timeout {
			<-ctx.Done()
			close(d.checked)
			return nil, ctx.Err()
		}
		select {
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		close(d.checked)
	}
	return d.fakeDSM.API(ctx, runner, op, params)
}

func TestNormalMoveNestedDSMTimeoutDoesNotCancel(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	d := &slowMoveDSM{fakeDSM: f, nested: true, checked: make(chan struct{})}
	e.DSM = d
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(id, "nested-timeout")); !r.OK {
		t.Fatal(r)
	}
	<-w.copying
	d.armed.Store(true)
	select {
	case <-d.checked:
	case <-time.After(7 * time.Second):
		t.Fatal("no validation")
	}
	time.Sleep(50 * time.Millisecond)
	close(w.release)
	waitState(t, e, "nested-timeout", "committed")
}

func TestNormalMoveSlowOrTimedOutRevalidationDoesNotCancel(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "slow", true: "deadline"}[timeout], func(t *testing.T) {
			e, f, _ := setup(t)
			if f.user.Admin {
				t.Fatal("requires normal subject")
			}
			id := longGrant(t, e)
			d := &slowMoveDSM{fakeDSM: f, timeout: timeout, checked: make(chan struct{})}
			e.DSM = d
			w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
			e.Writer = w
			if err := e.startOperations(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer e.stopOperations()
			if r := offline(e, "files.moveFromStaging", moveParams(id, "normal-long")); !r.OK {
				t.Fatal(r)
			}
			<-w.copying
			d.armed.Store(true)
			select {
			case <-d.checked:
			case <-time.After(moveValidationTimeout + 7*time.Second):
				t.Fatal("no periodic validation")
			}
			// Allow the returned validation result to reach its cancellation decision.
			time.Sleep(50 * time.Millisecond)
			close(w.release)
			waitState(t, e, "normal-long", "committed")
			grant, err := e.Store.Get(id)
			if err != nil || grant.Revoked {
				t.Fatal("transient failure revoked grant", grant, err)
			}
		})
	}
}

func TestMoveCancellationReportsReason(t *testing.T) {
	for _, action := range []string{"delegate.revoke", "delegate.disable", "files.cancelOperation"} {
		t.Run(action, func(t *testing.T) {
			e, f, _ := setup(t)
			id := longGrant(t, e)
			w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
			e.Writer = w
			if err := e.startOperations(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer e.stopOperations()
			if r := offline(e, "files.moveFromStaging", moveParams(id, "reason")); !r.OK {
				t.Fatal(r)
			}
			<-w.copying
			params := map[string]any{"delegation_id": id, "job_id": "reason"}
			if action == "delegate.disable" {
				f.user.Admin = true
				params = map[string]any{}
			}
			if r := request(e, action, params); !r.OK {
				t.Fatal(r)
			}
			waitState(t, e, "reason", "canceled")
			r := offline(e, "files.operationStatus", paramsForStatus(id, "reason"))
			b, _ := json.Marshal(r.Result)
			var out struct {
				Reason string `json:"reason"`
			}
			json.Unmarshal(b, &out)
			want := map[string]string{"delegate.revoke": "grant-revoked", "delegate.disable": "integration-disabled", "files.cancelOperation": "operation-canceled-by-user"}[action]
			if out.Reason != want {
				t.Fatal("missing readable reason", string(b))
			}
			select {
			case <-w.published:
				t.Fatal("published after cancellation")
			default:
			}
		})
	}
}

func TestNormalRevokeDuringBlockedValidationCancelsImmediately(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	d := &slowMoveDSM{fakeDSM: f, timeout: true, checked: make(chan struct{}), entered: make(chan struct{})}
	e.DSM = d
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(id, "blocked-revoke")); !r.OK {
		t.Fatal(r)
	}
	<-w.copying
	d.armed.Store(true)
	select {
	case <-d.entered:
	case <-time.After(7 * time.Second):
		t.Fatal("validation not entered")
	}
	if r := request(e, "delegate.revoke", map[string]any{"delegation_id": id}); !r.OK {
		t.Fatal(r)
	}
	op := waitState(t, e, "blocked-revoke", "canceled") // Two seconds, not the 30s validation budget.
	if op.Reason != "grant-revoked" {
		t.Fatal(op)
	}
	select {
	case <-w.published:
		t.Fatal("published after revoke")
	default:
	}
}
func paramsForStatus(id, job string) map[string]any {
	return map[string]any{"delegation_id": id, "job_id": job}
}
