package idgateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestResourceWaitsForReadinessAndCancels(t *testing.T) {
	up := newUpstream(t, nil)
	var ready atomic.Bool
	checked := make(chan struct{}, 1)
	f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) {
		c.Access = "public"
		c.Ready = func(context.Context) bool {
			select {
			case checked <- struct{}{}:
			default:
			}
			return ready.Load()
		}
	})
	r := httptest.NewRequest("GET", "https://nas.example/assets/app.js", nil)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { f.ServeHTTP(w, r); close(done) }()
	<-checked
	select {
	case <-done:
		t.Fatal("resource failed while App was starting")
	case <-time.After(30 * time.Millisecond):
	}
	ready.Store(true)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("resource did not resume")
	}
	if w.Code != 200 || w.Body.String() != "app" {
		t.Fatalf("resource: %d %s", w.Code, w.Body)
	}
	ready.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	r = httptest.NewRequest("HEAD", "https://nas.example/assets/app.css", nil).WithContext(ctx)
	done = make(chan struct{})
	go func() { f.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	<-checked
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled resource retained waiter")
	}
}

func TestBusyResourceSlotsWaitInsteadOfReturning503(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(fmt.Sprint(headers), func(t *testing.T) {
			up := newUpstream(t, nil)
			f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "public" })
			slots := f.resourceWaiters
			if headers {
				slots = f.proxy.Transport.(*boundedProxyTransport).headers
			}
			for range cap(slots) {
				slots <- struct{}{}
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				f.ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example/assets/app.js", nil))
				done <- w
			}()
			select {
			case w := <-done:
				t.Fatalf("busy asset returned early: %d", w.Code)
			case <-time.After(30 * time.Millisecond):
			}
			<-slots
			select {
			case w := <-done:
				if w.Code != 200 {
					t.Fatalf("resumed asset: %d %s", w.Code, w.Body)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("asset did not resume")
			}
			for len(slots) > 0 {
				<-slots
			}
		})
	}
}

func TestBusyResourceSlotWaitHonorsCancellation(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(fmt.Sprint(headers), func(t *testing.T) {
			up := newUpstream(t, nil)
			f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "public" })
			slots := f.resourceWaiters
			if headers {
				slots = f.proxy.Transport.(*boundedProxyTransport).headers
			}
			for range cap(slots) {
				slots <- struct{}{}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				f.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "https://nas.example/assets/app.js", nil).WithContext(ctx))
				close(done)
			}()
			select {
			case <-done:
				t.Fatal("busy asset failed before cancellation")
			case <-time.After(30 * time.Millisecond):
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("asset wait ignored cancellation")
			}
			if up.hits != 0 || len(slots) != cap(slots) {
				t.Fatal("canceled waiter consumed work")
			}
			for len(slots) > 0 {
				<-slots
			}
			if len(f.resourceWaiters) != 0 {
				t.Fatal("canceled header wait retained a resource slot")
			}
		})
	}
}

func TestResourceRevalidatesAfterWaitingForHeaderSlot(t *testing.T) {
	up := newUpstream(t, nil)
	f, clock := newFront(t, ModeNone, up, nil)
	b := signedBrowser(t, f, clock)
	slots := f.proxy.Transport.(*boundedProxyTransport).headers
	for range cap(slots) {
		slots <- struct{}{}
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- b.do("GET", "https://nas.example:18443/assets/app.js") }()
	select {
	case w := <-done:
		t.Fatalf("busy asset failed before revocation: %d", w.Code)
	case <-time.After(30 * time.Millisecond):
	}
	f.RevokeUID(alice.UID)
	<-slots
	w := <-done
	if w.Code != 401 || up.hits != 0 {
		t.Fatalf("revoked queued asset: %d upstream=%d", w.Code, up.hits)
	}
	for len(slots) > 0 {
		<-slots
	}
}

func TestResourceDialWaitRetriesOnlyBeforeConnection(t *testing.T) {
	up := newUpstream(t, nil)
	f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "public" })
	tr := f.proxy.Transport.(*boundedProxyTransport).base
	dial := tr.DialContext
	var calls atomic.Int32
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if calls.Add(1) == 1 {
			return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
		}
		return dial(ctx, network, address)
	}
	w := httptest.NewRecorder()
	f.ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example/assets/app.js", nil))
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("dial recovery: %d, calls %d", w.Code, calls.Load())
	}
}

func TestResourceNeverReplaysDeliveredRequest(t *testing.T) {
	for _, failure := range []error{errors.New("EOF"), &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}} {
		calls := 0
		tr := resourceTransport{RoundTripper: resourceRoundTrip(func(*http.Request) (*http.Response, error) { calls++; return nil, failure })}
		_, err := tr.RoundTrip(httptest.NewRequest("GET", "https://app.example/assets/app.js", nil))
		if err != failure || calls != 1 {
			t.Fatal("delivered request replayed", calls, err)
		}
	}
}

type resourceRoundTrip func(*http.Request) (*http.Response, error)

func (f resourceRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResourceWaitRevalidatesRevokedSession(t *testing.T) {
	up := newUpstream(t, nil)
	var ready atomic.Bool
	checked := make(chan struct{}, 1)
	f, clock := newFront(t, ModeNone, up, func(c *FrontConfig) {
		c.Ready = func(context.Context) bool {
			select {
			case checked <- struct{}{}:
			default:
			}
			return ready.Load()
		}
	})
	b := signedBrowser(t, f, clock)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- b.do("GET", "https://nas.example:18443/assets/app.js") }()
	<-checked
	f.RevokeUID(alice.UID)
	ready.Store(true)
	select {
	case w := <-done:
		if w.Code != 401 || up.hits != 0 {
			t.Fatalf("revoked resource admitted: %d hits=%d", w.Code, up.hits)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("resource did not finish")
	}
}

func TestResourceRefusesObsoleteInjectedAdministratorRole(t *testing.T) {
	up := newUpstream(t, nil)
	f, clock := newFront(t, ModeHeader, up, func(c *FrontConfig) { c.Access = "dsm-users"; c.Membership = &independentRole{admin: false} })
	b := signedBrowser(t, f, clock)
	w := b.do("GET", "https://nas.example:18443/assets/app.js")
	if w.Code != 401 || up.hits != 0 {
		t.Fatalf("obsolete admin role reached upstream: %d hits=%d", w.Code, up.hits)
	}
}

func TestDaemonReadinessCancellationClosesPendingSocket(t *testing.T) {
	path := t.TempDir() + "/status.sock"
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := l.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- daemonReadiness(path, "digest", time.Minute)(ctx) }()
	conn := <-accepted
	defer conn.Close()
	cancel()
	select {
	case ready := <-done:
		if ready {
			t.Fatal("canceled status admitted")
		}
	case <-time.After(time.Second):
		t.Fatal("status read ignored cancellation")
	}
}

func TestUpstreamHeaderWorkIsBoundedAndCanceled(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "public" })
	tr := f.proxy.Transport.(*boundedProxyTransport)
	for range cap(tr.headers) {
		tr.headers <- struct{}{}
	}
	w := newBrowser(t, f).do("GET", "https://nas.example:18443/api/items")
	if w.Code != 503 || up.hits != 0 {
		t.Fatalf("header work exceeded bound: %d", w.Code)
	}
	for range cap(tr.headers) {
		<-tr.headers
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		f.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "https://nas.example/assets/app.js", nil).WithContext(ctx))
		close(done)
	}()
	deadline := time.After(time.Second)
	for len(tr.headers) == 0 {
		select {
		case <-deadline:
			t.Fatal("request did not occupy header slot")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("header wait ignored cancellation")
	}
	if len(tr.headers) != 0 || len(f.resourceWaiters) != 0 {
		t.Fatal("cancellation leaked work slots")
	}
}

func TestUpstreamHeaderSlotReleasedBeforeBodyStreams(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.Access = "public" })
	tr := f.proxy.Transport.(*boundedProxyTransport)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", up.URL, nil)
	res, err := tr.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if len(tr.headers) != 0 {
		t.Fatal("streaming body retained header slot")
	}
	cancel()
	_, _ = io.Copy(io.Discard, res.Body)
}
