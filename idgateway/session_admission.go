package idgateway

import (
	"context"
	"errors"
	"net/http"
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
}

type sessionCheck struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	err     error
}

// shareSessionCheck retains only work that is still running. Each caller can
// disconnect independently; the last waiter cancels the native work. There is
// no admission TTL and no queue timeout that could turn a busy page into 503.
func (f *Front) shareSessionCheck(ctx context.Context, key sessionCheckKey, work func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.checkMu.Lock()
	check := f.checks[key]
	if check == nil {
		shared, cancel := context.WithCancel(context.WithoutCancel(ctx))
		check = &sessionCheck{done: make(chan struct{}), cancel: cancel}
		if f.checks == nil {
			f.checks = make(map[sessionCheckKey]*sessionCheck)
		}
		f.checks[key] = check
		go func() {
			err := work(shared)
			f.checkMu.Lock()
			check.err = err
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
		return ctx.Err()
	case <-check.done:
		return check.err
	}
}

func (f *Front) checkSession(ctx context.Context, id, ip string, injectedRole bool) error {
	f.mu.Lock()
	s, ok := f.sessions[id]
	f.mu.Unlock()
	if !ok || s.ip != ip || !f.cfg.Now().Before(s.expires) || f.cfg.Accounts == nil {
		return errSessionChanged
	}
	err := f.shareSessionCheck(ctx, sessionCheckKey{id, ip, injectedRole}, func(shared context.Context) error {
		if err := f.admitClient(shared, s.subject, ip); err != nil {
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
	name, err := f.cfg.Accounts.Name(ctx, s.subject.UID)
	if err != nil || name != s.subject.Name {
		return errSessionChanged
	}
	f.mu.Lock()
	current, ok := f.sessions[id]
	f.mu.Unlock()
	if !ok || current != s || !f.cfg.Now().Before(s.expires) {
		return errSessionChanged
	}
	return ctx.Err()
}
