package delegate

import (
	"context"
	"errors"
	"time"
)

const executorWaitLimit = 15 * time.Minute

func (s *Service) clockNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
func (s *Service) waitExecutor(ctx context.Context, delay time.Duration) error {
	if s.wait != nil {
		return s.wait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Retry only failures that prove no executor connection was established. Once
// connected, even an uncertain/partial write is terminal and never replayed.
func (s *Service) callWaitingExecutor(ctx context.Context, id string, expires time.Time, op string, params map[string]any, out any) error {
	var deadline time.Time
	delay := 2 * time.Second
	waiting := false
	for {
		if ctx.Err() != nil {
			return fail("canceled")
		}
		now := s.clockNow()
		if !expires.IsZero() && !now.Before(expires) {
			return fail("DELEGATION_EXPIRED")
		}
		if waiting && !now.Before(deadline) {
			return fail(CodeExecutorUnavailable)
		}
		binding, err := s.Store.Delegation(ctx, id)
		if err != nil {
			return err
		}
		if binding.DelegationID != params["delegation_id"] {
			return fail(CodeInvalidRequest)
		}
		state, err := s.Store.Get(ctx, id)
		if err != nil {
			return err
		}
		if state.CancelRequested || state.PurgeRequested {
			return fail("canceled")
		}
		callCtx := ctx
		cancel := func() {}
		if waiting {
			callCtx, cancel = context.WithTimeout(ctx, deadline.Sub(now))
		}
		err = s.Client.Call(callCtx, op, nil, params, out)
		cancel()
		var dial *dialFailure
		if !errors.As(err, &dial) {
			if err == nil && waiting {
				// A racing cancellation still wins in Store.Finish.
				_ = s.Store.SetExecutorWaiting(ctx, id, false)
			}
			return err
		}
		if !waiting {
			waiting = true
			deadline = s.clockNow().Add(executorWaitLimit)
			if err = s.Store.SetExecutorWaiting(ctx, id, true); err != nil {
				return err
			}
		}
		now = s.clockNow()
		remaining := deadline.Sub(now)
		if !expires.IsZero() && expires.Sub(now) < remaining {
			remaining = expires.Sub(now)
		}
		if remaining <= 0 {
			continue
		}
		pause := delay
		if pause > remaining {
			pause = remaining
		}
		if err = s.waitExecutor(ctx, pause); err != nil {
			return fail("canceled")
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}
