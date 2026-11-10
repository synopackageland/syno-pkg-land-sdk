package idgateway

import (
	"context"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"net/http"
	"sync"
)

var errSessionChanged = errors.New("gateway session is no longer current")

// Assertion bootstrap uses admit directly. An authenticated request must never
// fall back to bootstrap admission if its session disappears between checks.
func (f *Front) admitSession(r *http.Request) error {
	cookie, err := r.Cookie(f.sessionCookie(r))
	if err != nil {
		return errSessionChanged
	}
	return f.checkSession(r.Context(), cookie.Value, remoteIP(r), false)
}

type sessionCheckKey struct {
	id, ip       string
	injectedRole bool
	phase        string
}

type verificationChecks[T any] struct {
	checkMu sync.Mutex
	checks  map[sessionCheckKey]*sessionCheck[T]
}

type sessionCheck[T any] struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	err     error
	value   T
}

// do retains only work that is still running. Each caller can
// disconnect independently; the last waiter cancels the native work. There is
// no admission TTL and no queue timeout that could turn a busy page into 503.
func (f *verificationChecks[T]) do(ctx context.Context, key sessionCheckKey, work func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	f.checkMu.Lock()
	check := f.checks[key]
	if check == nil {
		shared, cancel := context.WithTimeout(context.WithoutCancel(ctx), runtimebudget.Operation)
		check = &sessionCheck[T]{done: make(chan struct{}), cancel: cancel}
		if f.checks == nil {
			f.checks = make(map[sessionCheckKey]*sessionCheck[T])
		}
		f.checks[key] = check
		go func() {
			value, err := work(shared)
			f.checkMu.Lock()
			check.err, check.value = err, value
			if f.checks[key] == check {
				delete(f.checks, key)
			}
			close(check.done)
			f.checkMu.Unlock()
			cancel()
		}()
	}
	check.waiters++
	f.checkMu.Unlock()
	defer func() {
		f.checkMu.Lock()
		check.waiters--
		if check.waiters == 0 {
			if f.checks[key] == check {
				delete(f.checks, key)
			}
			check.cancel()
		}
		f.checkMu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-check.done:
		return check.value, check.err
	}
}

func (f *Front) shareSessionCheck(ctx context.Context, key sessionCheckKey, work func(context.Context) error) error {
	_, err := f.verificationChecks.do(ctx, key, func(shared context.Context) (struct{}, error) {
		return struct{}{}, retryVerification(shared, work)
	})
	return err
}

func (f *Front) checkSession(ctx context.Context, id, ip string, injectedRole bool) error {
	ctx, cancel := context.WithTimeout(ctx, runtimebudget.Operation)
	defer cancel()
	f.mu.Lock()
	s, ok := f.sessions[id]
	f.mu.Unlock()
	if !ok || s.ip != ip || !f.cfg.Now().Before(s.expires) || f.cfg.Accounts == nil {
		return errSessionChanged
	}
	err := f.shareSessionCheck(ctx, sessionCheckKey{id: id, ip: ip, injectedRole: injectedRole}, func(shared context.Context) error {
		if err := f.admitClientOnce(shared, s.subject, ip); err != nil {
			return err
		}
		if injectedRole && (f.cfg.Mode == ModeToken || f.cfg.Mode == ModeHeader && f.cfg.Headers.Groups != "") {
			admin, err := administrator(shared, s.subject.Name, f.cfg.Membership)
			if err != nil {
				return err
			}
			if admin != s.subject.Admin {
				return errSessionChanged
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Work may have waited behind slow DSM lookups. Every waiter independently
	// rechecks account identity, expiry, address and revocation before forwarding.
	if err := f.checkAccount(ctx, id, ip, s); err != nil {
		return err
	}
	f.mu.Lock()
	current, ok := f.sessions[id]
	f.mu.Unlock()
	if !ok || current != s || !f.cfg.Now().Before(s.expires) {
		return errSessionChanged
	}
	return ctx.Err()
}

func (f *Front) checkAccount(ctx context.Context, id, ip string, s session) error {
	return f.shareSessionCheck(ctx, sessionCheckKey{id: id, ip: ip, phase: "account"}, func(ctx context.Context) error {
		name, err := f.cfg.Accounts.Name(ctx, s.subject.UID)
		if errors.Is(err, ErrNoAccount) || err == nil && name != s.subject.Name {
			return errSessionChanged
		}
		return err
	})
}
