package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// timedBody records when something first read the request body.
type timedBody struct {
	io.Reader
	at time.Time
}

func (b *timedBody) Read(p []byte) (int, error) {
	if b.at.IsZero() {
		b.at = time.Now()
	}
	return b.Reader.Read(p)
}
func (b *timedBody) Close() error { return nil }

// timedAuth records the ingress time it sees and when it ran.
type timedAuth struct {
	fakeAuth
	ingress, at time.Time
}

func (a *timedAuth) Authenticate(r *http.Request) (Identity, error) {
	a.ingress, a.at = IngressTime(r.Context()), time.Now()
	return a.fakeAuth.Authenticate(r)
}

func timedServer(t *testing.T, auth Authenticator, inHandler *time.Time) *Server {
	t.Helper()
	return newTestServer(t, auth, Handle("demo.admin", func(ctx context.Context, _ Caller, _ json.RawMessage) (any, error) {
		*inHandler = IngressTime(ctx)
		return "ok", nil
	}))
}

func TestIngressTimeIsRecordedBeforeBodyAndSession(t *testing.T) {
	auth := &timedAuth{fakeAuth: fakeAuth{user: "alice", admin: true}}
	var inHandler time.Time
	srv := timedServer(t, auth, &inHandler)
	var body *timedBody
	before := time.Now()
	rec, env := post(t, srv, `{"method":"demo.admin","params":{}}`, func(r *http.Request) {
		body = &timedBody{Reader: r.Body}
		r.Body = body
	})
	if rec.Code != http.StatusOK || !env.Success {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	at := auth.ingress
	if at.IsZero() || at.Before(before) || at.After(body.at) || at.After(auth.at) || !inHandler.Equal(at) {
		t.Fatalf("ingress %v, before %v, body read %v, auth %v, handler %v", at, before, body.at, auth.at, inHandler)
	}
}

func TestIngressTimeComesFromTheOutermostSDKHandler(t *testing.T) {
	auth := &timedAuth{fakeAuth: fakeAuth{user: "alice", admin: true}}
	var inHandler, inObserver time.Time
	var outcome Outcome
	srv := timedServer(t, auth, &inHandler)
	var outer time.Time
	h := WithRequestDeadline(WithObserver(WithDescriptionData(srv, auth, []byte(`{}`)), func(r *http.Request, o Outcome) {
		inObserver, outcome = IngressTime(r.Context()), o
	}))
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outer = IngressTime(r.Context())
		h.ServeHTTP(w, r)
	})
	// The probe sits outside every SDK handler, so it sees no ingress time.
	post(t, probe, `{"method":"demo.admin","params":{}}`)
	if !outer.IsZero() {
		t.Fatal("ingress time set outside the SDK")
	}
	at := auth.ingress
	if at.IsZero() || !inHandler.Equal(at) || !inObserver.Equal(at) {
		t.Fatalf("auth %v, handler %v, observer %v", at, inHandler, inObserver)
	}
	if outcome.Duration < auth.at.Sub(at) {
		t.Fatalf("observer duration %v does not start at ingress", outcome.Duration)
	}
}

func TestIngressTimeIsSetForRefusedRequests(t *testing.T) {
	auth := &timedAuth{fakeAuth: fakeAuth{err: ErrNoSession}}
	var inHandler time.Time
	_, env := post(t, timedServer(t, auth, &inHandler), `{"method":"demo.admin","params":{}}`)
	if env.Success || env.Error == nil || env.Error.Code != "session_required" || auth.ingress.IsZero() || !inHandler.IsZero() {
		t.Fatalf("got %+v, auth saw %v, handler ran %v", env, auth.ingress, inHandler)
	}
}

func TestIngressTimeOnTheDaemonIngress(t *testing.T) {
	var seen time.Time
	h := ingressHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = IngressTime(r.Context())
	}), "/synopkgland/SampleApp/", ingressOptions{request: time.Minute, stream: time.Minute})
	r := httptest.NewRequest("POST", "/synopkgland/SampleApp/api", strings.NewReader("{}"))
	r.Header.Set("X-Real-IP", "192.0.2.1")
	before := time.Now()
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen.Before(before) || seen.After(time.Now()) {
		t.Fatalf("ingress time %v", seen)
	}
}

func TestIngressTimeIsZeroOutsideTheSDK(t *testing.T) {
	if !IngressTime(nil).IsZero() || !IngressTime(context.Background()).IsZero() {
		t.Fatal("ingress time without an SDK handler")
	}
}
