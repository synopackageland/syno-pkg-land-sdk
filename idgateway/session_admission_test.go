package idgateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitSessionChecks(t *testing.T, f *Front, want int) {
	t.Helper()
	until := time.After(3 * time.Second)
	for {
		f.checkMu.Lock()
		got := 0
		for _, c := range f.checks {
			got += c.waiters
		}
		f.checkMu.Unlock()
		if got == want {
			return
		}
		select {
		case <-until:
			t.Fatalf("verification waiters=%d, want %d", got, want)
		case <-time.After(time.Millisecond):
		}
	}
}

type contendedRole struct {
	active, calls atomic.Int32
	started       chan struct{}
	release       chan struct{}
}

func (m *contendedRole) Groups(context.Context, string) ([]string, error) { return nil, nil }
func (m *contendedRole) IsAdministrator(ctx context.Context, _ string) (bool, error) {
	m.calls.Add(1)
	if m.active.Add(1) != 1 {
		m.active.Add(-1)
		return false, errors.New("native role work is busy")
	}
	defer m.active.Add(-1)
	select {
	case m.started <- struct{}{}:
	default:
	}
	select {
	case <-m.release:
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// All requests carry an already authenticated session. A burst must share
// active verification instead of multiplying native processes and returning 503.
func TestConcurrentSessionAdmissionSharesNativeVerification(t *testing.T) {
	m := &contendedRole{started: make(chan struct{}, 1), release: make(chan struct{})}
	f, clock := newFront(t, ModeNone, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = m })
	f.sessions["session"] = session{subject: alice, ip: "192.0.2.7", expires: clock.now().Add(time.Hour)}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan int, 40)
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			b := newBrowser(t, f)
			b.cookies[f.cookie] = "session"
			results <- b.do("GET", "https://nas.example/api/read").Code
		}()
	}
	close(start)
	<-m.started
	// Hold the native operation while the entire HTTP burst reaches admission.
	waitSessionChecks(t, f, 40)
	close(m.release)
	wg.Wait()
	close(results)
	for status := range results {
		if status != http.StatusOK {
			t.Errorf("concurrent admission returned %d", status)
		}
	}
	if calls := m.calls.Load(); calls != 1 {
		t.Errorf("burst ran %d native verifications, want one", calls)
	}
	// Completed results are never retained: the next request checks DSM again.
	before := m.calls.Load()
	b := newBrowser(t, f)
	b.cookies[f.cookie] = "session"
	if w := b.do("GET", "https://nas.example/api/read"); w.Code != 200 || m.calls.Load() != before+1 {
		t.Fatal("completed admission was cached")
	}
}

func admissionRequest(f *Front, ctx context.Context) *http.Request {
	r := httptest.NewRequest("GET", "https://nas.example/api/read", nil).WithContext(ctx)
	r.RemoteAddr = "192.0.2.7:4444"
	r.AddCookie(&http.Cookie{Name: f.cookie, Value: "session"})
	return r
}

func TestSessionAdmissionCancellationIsPerWaiter(t *testing.T) {
	m := &contendedRole{started: make(chan struct{}, 1), release: make(chan struct{})}
	f, clock := newFront(t, ModeNone, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = m })
	f.sessions["session"] = session{subject: alice, ip: "192.0.2.7", expires: clock.now().Add(time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- f.admitSession(admissionRequest(f, ctx)) }()
	<-m.started
	go func() { second <- f.admitSession(admissionRequest(f, context.Background())) }()
	waitSessionChecks(t, f, 2)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter: %v", err)
	}
	if m.active.Load() != 1 {
		t.Fatal("one canceled request stopped another request's verification")
	}
	close(m.release)
	if err := <-second; err != nil || m.calls.Load() != 1 {
		t.Fatalf("surviving waiter: %v calls=%d", err, m.calls.Load())
	}
}

func TestLastAdmissionWaiterCancelsNativeWorkAndAllowsFreshCheck(t *testing.T) {
	m := &contendedRole{started: make(chan struct{}, 1), release: make(chan struct{})}
	f, clock := newFront(t, ModeNone, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = m })
	f.sessions["session"] = session{subject: alice, ip: "192.0.2.7", expires: clock.now().Add(time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.admitSession(admissionRequest(f, ctx)) }()
	<-m.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	until := time.After(3 * time.Second)
	for m.active.Load() != 0 {
		select {
		case <-until:
			t.Fatal("abandoned native lookup remained active")
		case <-time.After(time.Millisecond):
		}
	}
	waitSessionChecks(t, f, 0)
	close(m.release)
	if err := f.admitSession(admissionRequest(f, context.Background())); err != nil || m.calls.Load() != 2 {
		t.Fatalf("fresh lookup: %v calls=%d", err, m.calls.Load())
	}
}

type admissionRoleFunc func(context.Context, string) (bool, error)

type admissionNamesFunc func(context.Context, int) (string, error)

func (f admissionNamesFunc) Name(ctx context.Context, uid int) (string, error) {
	return f(ctx, uid)
}

func TestSessionRevokedBetweenLookupAndAdmissionCannotUseBootstrap(t *testing.T) {
	var calls atomic.Int32
	m := admissionRoleFunc(func(context.Context, string) (bool, error) { calls.Add(1); return true, nil })
	up := newUpstream(t, nil)
	f, clock := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = m })
	f.sessions["session"] = session{subject: alice, ip: "192.0.2.7", expires: clock.now().Add(time.Hour)}
	f.cfg.Accounts = admissionNamesFunc(func(context.Context, int) (string, error) { f.RevokeUID(alice.UID); return alice.Name, nil })
	b := newBrowser(t, f)
	b.cookies[f.cookie] = "session"
	if w := b.do("GET", "https://nas.example/api/read"); w.Code != 401 || calls.Load() != 0 || up.hits != 0 {
		t.Fatalf("revoked session fell back to bootstrap: status=%d checks=%d hits=%d", w.Code, calls.Load(), up.hits)
	}
}

func (f admissionRoleFunc) Groups(context.Context, string) ([]string, error) { return nil, nil }
func (f admissionRoleFunc) IsAdministrator(ctx context.Context, name string) (bool, error) {
	return f(ctx, name)
}

func TestDistinctSessionsDoNotShareAdmission(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	m := admissionRoleFunc(func(ctx context.Context, _ string) (bool, error) {
		calls.Add(1)
		select {
		case <-release:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	})
	f, clock := newFront(t, ModeNone, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = m })
	for _, id := range []string{"one", "two"} {
		f.sessions[id] = session{subject: alice, ip: "192.0.2.7", expires: clock.now().Add(time.Hour)}
	}
	done := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		go func() {
			r := admissionRequest(f, context.Background())
			r.Header.Set("Cookie", f.cookie+"="+id)
			done <- f.admitSession(r)
		}()
	}
	waitSessionChecks(t, f, 2)
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("distinct sessions shared native authority: %d", calls.Load())
	}
}

func TestAdmissionFailureRemainsDiagnosableAndIsNotCached(t *testing.T) {
	var calls atomic.Int32
	m := admissionRoleFunc(func(context.Context, string) (bool, error) {
		if calls.Add(1) == 1 {
			return false, errors.New("native verification unavailable")
		}
		return true, nil
	})
	f, clock := newFront(t, ModeNone, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = m })
	f.sessions["session"] = session{subject: alice, ip: "192.0.2.7", expires: clock.now().Add(time.Hour)}
	b := newBrowser(t, f)
	b.cookies[f.cookie] = "session"
	w := b.do("GET", "https://nas.example/api/read")
	if w.Code != 503 || w.Header().Get("X-Synopkgland-Error-Stage") != "admission" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("undifferentiated refusal: %d %v", w.Code, w.Header())
	}
	if w := b.do("GET", "https://nas.example/api/read"); w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("error retained: %d calls=%d", w.Code, calls.Load())
	}
}

func TestSessionExpiresOrAccountChangesWhileAdmissionWaits(t *testing.T) {
	for _, change := range []string{"expiry", "account"} {
		t.Run(change, func(t *testing.T) {
			m := &contendedRole{started: make(chan struct{}, 1), release: make(chan struct{})}
			up := newUpstream(t, nil)
			f, clock := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = m })
			f.sessions["session"] = session{subject: alice, ip: "192.0.2.7", expires: clock.now().Add(time.Minute)}
			b := newBrowser(t, f)
			b.cookies[f.cookie] = "session"
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- b.do("GET", "https://nas.example/api/read") }()
			<-m.started
			if change == "expiry" {
				clock.t = clock.t.Add(time.Hour)
			} else {
				f.cfg.Accounts.(fakeNames)[alice.UID] = "renamed"
			}
			close(m.release)
			w := <-done
			if w.Code != 401 || up.hits != 0 {
				t.Fatalf("changed authority admitted: %d upstream=%d", w.Code, up.hits)
			}
		})
	}
}

func TestConcurrentResourceAndUpgradeChecksShareOnlyActiveWork(t *testing.T) {
	m := &contendedRole{started: make(chan struct{}, 1), release: make(chan struct{})}
	f, clock := newFront(t, ModeHeader, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = m })
	f.sessions["session"] = session{subject: alice, ip: "192.0.2.7", expires: clock.now().Add(time.Hour)}
	done := make(chan bool, 40)
	for range 40 {
		go func() { done <- f.upgradeAllowed(context.Background(), "session", "192.0.2.7") }()
	}
	<-m.started
	waitSessionChecks(t, f, 40)
	close(m.release)
	for range 40 {
		if !<-done {
			t.Fatal("concurrent revalidation failed")
		}
	}
	if m.calls.Load() != 2 {
		t.Fatalf("injected role checks ran %d native verifications", m.calls.Load())
	}
}

func TestSessionRevokedDuringAdmissionCannotReachUpstream(t *testing.T) {
	m := &contendedRole{started: make(chan struct{}, 1), release: make(chan struct{})}
	up := newUpstream(t, nil)
	f, clock := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = m })
	f.sessions["session"] = session{subject: alice, ip: "192.0.2.7", expires: clock.now().Add(time.Hour)}
	b := newBrowser(t, f)
	b.cookies[f.cookie] = "session"
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- b.do("GET", "https://nas.example/api/read") }()
	<-m.started
	f.RevokeUID(alice.UID)
	close(m.release)
	w := <-done
	if w.Code != http.StatusUnauthorized || up.hits != 0 {
		t.Fatalf("revoked request: status=%d upstream=%d", w.Code, up.hits)
	}
}
