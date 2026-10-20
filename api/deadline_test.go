package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type deadlineAuth struct {
	t   *testing.T
	ctx context.Context
}

func (a *deadlineAuth) Authenticate(r *http.Request) (Identity, error) {
	a.t.Helper()
	d, ok := r.Context().Deadline()
	if !ok || time.Until(d) > DefaultRequestTimeout || time.Until(d) <= 0 {
		a.t.Fatalf("authentication deadline: %v %v", d, ok)
	}
	a.ctx = r.Context()
	return Identity{User: "alice", Admin: true}, nil
}
func TestRequestDeadlineReachesAuthenticationAndAllHandlers(t *testing.T) {
	for _, kind := range []string{"json", "request", "response", "stream"} {
		t.Run(kind, func(t *testing.T) {
			auth := &deadlineAuth{t: t}
			called := false
			check := func(ctx context.Context, r *http.Request) {
				t.Helper()
				called = true
				if ctx != auth.ctx || (r != nil && r.Context() != ctx) {
					t.Fatal("context not propagated")
				}
			}
			var route Route
			switch kind {
			case "json":
				route = Handle("demo.deadline", func(ctx context.Context, _ Caller, _ json.RawMessage) (any, error) { check(ctx, nil); return nil, nil })
			case "request":
				route = HandleRequest("demo.deadline", func(ctx context.Context, _ Caller, r *http.Request, _ json.RawMessage) (any, error) {
					check(ctx, r)
					return nil, nil
				})
			case "response":
				route = HandleResponse("demo.deadline", func(ctx context.Context, _ Caller, _ http.ResponseWriter, r *http.Request, _ json.RawMessage) error {
					check(ctx, r)
					return nil
				})
			case "stream":
				route = HandleStream("demo.deadline", func(ctx context.Context, _ Caller, _ http.ResponseWriter, r *http.Request) error {
					check(ctx, r)
					return nil
				})
			}
			srv := newTestServer(t, auth, route)
			target := "/api.cgi"
			if kind == "stream" {
				target += "?method=demo.deadline"
			}
			r := httptest.NewRequest("POST", target, strings.NewReader(`{"method":"demo.deadline"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "http://example.com")
			srv.ServeHTTP(httptest.NewRecorder(), r)
			if !called || auth.ctx.Err() != context.Canceled {
				t.Fatalf("called=%v context=%v", called, auth.ctx)
			}
		})
	}
}
func TestEarlierRequestDeadlinePreserved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	srv := newTestServer(t, &fakeAuth{admin: true}, Handle("demo.wait", func(c context.Context, _ Caller, _ json.RawMessage) (any, error) {
		want, _ := ctx.Deadline()
		got, ok := c.Deadline()
		if !ok || !got.Equal(want) {
			t.Fatal("earlier deadline lost")
		}
		<-c.Done()
		return nil, c.Err()
	}))
	r := httptest.NewRequest("POST", "/api.cgi", strings.NewReader(`{"method":"demo.wait"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `"request_timeout"`) {
		t.Fatal(w.Body.String())
	}
}

func TestCanceledRequestHasItsOwnCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := newTestServer(t, &fakeAuth{admin: true}, Handle("demo.wait", func(c context.Context, _ Caller, _ json.RawMessage) (any, error) {
		cancel()
		return nil, c.Err()
	}))
	r := httptest.NewRequest("POST", "/api.cgi", strings.NewReader(`{"method":"demo.wait"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `"request_canceled"`) {
		t.Fatal(w.Body.String())
	}
}

func TestAppSetsTheRequestDeadline(t *testing.T) {
	var deadline time.Time
	srv := newTestServer(t, &fakeAuth{admin: true}, Handle("demo.deadline", func(ctx context.Context, _ Caller, _ json.RawMessage) (any, error) {
		deadline, _ = ctx.Deadline()
		return nil, nil
	}))
	r := httptest.NewRequest("POST", "/api.cgi", strings.NewReader(`{"method":"demo.deadline"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://example.com")
	start := time.Now()
	WithRequestDeadline(srv, Timeout(90*time.Second)).ServeHTTP(httptest.NewRecorder(), r)
	if got := deadline.Sub(start); got < 89*time.Second || got > 91*time.Second {
		t.Fatalf("deadline in %v, want 90s", got)
	}
}

func TestServerAloneKeepsTheDefaultDeadline(t *testing.T) {
	var deadline time.Time
	srv := newTestServer(t, &fakeAuth{admin: true}, Handle("demo.deadline", func(ctx context.Context, _ Caller, _ json.RawMessage) (any, error) {
		deadline, _ = ctx.Deadline()
		return nil, nil
	}))
	r := httptest.NewRequest("POST", "/api.cgi", strings.NewReader(`{"method":"demo.deadline"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://example.com")
	start := time.Now()
	srv.ServeHTTP(httptest.NewRecorder(), r)
	if got := deadline.Sub(start); got < DefaultRequestTimeout || got > DefaultRequestTimeout+time.Second {
		t.Fatalf("deadline in %v, want %v", got, DefaultRequestTimeout)
	}
}

func TestRequestTimeoutMustBePositive(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("accepted a zero timeout")
		}
	}()
	WithRequestDeadline(http.NotFoundHandler(), Timeout(0))
}

func TestCGIDeadlineIncludesOuterIdentityMiddleware(t *testing.T) {
	var outerDeadline time.Time
	srv := newTestServer(t, &fakeAuth{admin: true}, Handle("demo.deadline", func(ctx context.Context, _ Caller, _ json.RawMessage) (any, error) {
		d, _ := ctx.Deadline()
		if !d.Equal(outerDeadline) {
			t.Fatal("API restarted the outer CGI budget")
		}
		return nil, nil
	}))
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		outerDeadline, ok = r.Context().Deadline()
		if !ok || time.Until(outerDeadline) > DefaultRequestTimeout {
			t.Fatal("outer middleware has no CGI deadline")
		}
		srv.ServeHTTP(w, r)
	})
	r := httptest.NewRequest("POST", "/api.cgi", strings.NewReader(`{"method":"demo.deadline"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://example.com")
	WithRequestDeadline(outer).ServeHTTP(httptest.NewRecorder(), r)
}

func TestDefaultRequestTimeoutIsSixtySeconds(t *testing.T) {
	if DefaultRequestTimeout != 60*time.Second {
		t.Fatalf("DefaultRequestTimeout = %v, want 60s", DefaultRequestTimeout)
	}
}
