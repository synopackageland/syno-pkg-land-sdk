package api

import (
	"bufio"
	"context"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// Outcome is what the SDK decided about one request to the App's server, as
// WithObserver reports it after the response: also for requests the SDK
// refused before any App route ran.
type Outcome struct {
	// Method is the App method the request named; "" for a batch or for a
	// request that named none.
	Method string
	// Batch lists the methods of a batch request, in order.
	Batch []string
	// User is the DSM account an authenticator verified, also when an entry
	// rule or the route's permission then refused it; "" without a session.
	User string
	// Admin is true only when the request was admitted as an administrator.
	Admin bool
	// Code is the error code of the SDK's or the route's refusal; "" when the
	// request succeeded or a raw handler answered it itself.
	Code string
	// Status is the HTTP status written; 200 when nothing set one.
	Status   int
	Duration time.Duration
}

type observation struct {
	mu      sync.Mutex
	method  string
	batch   []string
	inBatch bool
	user    string
	admin   bool
	code    string
}

type observationKey struct{}

// observedResponse lets the SDK's error writers record their code. Unwrap,
// Flush and Hijack keep streaming and upgrade handlers working.
type observedResponse struct {
	http.ResponseWriter
	seen   *observation
	status int
}

func (w *observedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *observedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *observedResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}
func (w *observedResponse) Flush() { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w *observedResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// WithObserver reports each request's Outcome to observe after next has
// answered it. Wrap the App's Server with it (inside WithRequestDeadline, the
// same way), for example to audit refused sign-ins. observe runs after the
// response and cannot change it; a panic in observe is logged and dropped.
func WithObserver(next http.Handler, observe func(*http.Request, Outcome)) http.Handler {
	return &observerHandler{next: next, observe: observe}
}

type observerHandler struct {
	next    http.Handler
	observe func(*http.Request, Outcome)
}

func (h *observerHandler) Unwrap() http.Handler { return h.next }
func (h *observerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	seen := &observation{}
	r = r.WithContext(context.WithValue(r.Context(), observationKey{}, seen))
	response := &observedResponse{ResponseWriter: w, seen: seen}
	started := time.Now()
	h.next.ServeHTTP(response, r)
	if h.observe == nil {
		return
	}
	seen.mu.Lock()
	o := Outcome{Method: seen.method, Batch: append([]string(nil), seen.batch...), User: seen.user, Admin: seen.admin, Code: seen.code, Status: response.status, Duration: time.Since(started)}
	seen.mu.Unlock()
	if o.Status == 0 {
		o.Status = http.StatusOK
	}
	if o.Duration <= 0 {
		o.Duration = time.Nanosecond
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("request observer panic: %v", p)
		}
	}()
	h.observe(r, o)
}

// IntegrationRoute and HTTPRoute let SDK middleware look through the observer.
func (h *observerHandler) IntegrationRoute(r *http.Request) string {
	return IntegrationRouteMethod(h.next, r)
}
func (h *observerHandler) HTTPRoute(r *http.Request) string { return HTTPRouteMethod(h.next, r) }

func observationOf(r *http.Request) *observation {
	if r == nil {
		return nil
	}
	seen, _ := r.Context().Value(observationKey{}).(*observation)
	return seen
}

// ObserveIdentity records the account an Authenticator verified for the
// request's Outcome, before an entry rule may refuse it. Authenticators that
// refuse a verified account call it, as dsm.EntryGate does. It grants nothing:
// admission is still only what Authenticate returns. Without WithObserver it
// does nothing.
func ObserveIdentity(r *http.Request, id Identity) {
	if seen := observationOf(r); seen != nil && id.User != "" {
		seen.mu.Lock()
		seen.user = id.User
		seen.mu.Unlock()
	}
}

func noteMethod(r *http.Request, method string) {
	if seen := observationOf(r); seen != nil {
		seen.mu.Lock()
		defer seen.mu.Unlock()
		if seen.inBatch {
			if len(seen.batch) < MaxBatchMethods {
				seen.batch = append(seen.batch, method)
			}
			return
		}
		seen.method = method
	}
}

func noteBatch(r *http.Request) {
	if seen := observationOf(r); seen != nil {
		seen.mu.Lock()
		seen.inBatch, seen.method = true, ""
		seen.mu.Unlock()
	}
}

func noteCaller(r *http.Request, id Identity) {
	if seen := observationOf(r); seen != nil {
		seen.mu.Lock()
		seen.user, seen.admin = id.User, id.Admin
		seen.mu.Unlock()
	}
}

// noteCode finds the observer through any SDK response wrappers.
func noteCode(w http.ResponseWriter, code string) {
	for range 16 {
		if o, ok := w.(*observedResponse); ok {
			o.seen.mu.Lock()
			o.seen.code = code
			o.seen.mu.Unlock()
			return
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = u.Unwrap()
	}
}
