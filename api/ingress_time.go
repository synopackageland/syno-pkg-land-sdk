package api

import (
	"context"
	"net/http"
	"time"
)

type ingressTimeKey struct{}

// IngressTime is when the request reached the SDK: the outermost SDK handler
// (ServeIngress, WithRequestDeadline, WithObserver, WithDescription or
// Server) records it before it reads the body, checks the origin or
// authenticates the session. Handlers and Authenticators read it from the
// request context, for example to measure a response-time budget. It is the
// zero time for a context that did not pass through an SDK handler, such as
// one a test builds.
func IngressTime(ctx context.Context) time.Time {
	if ctx == nil {
		return time.Time{}
	}
	t, _ := ctx.Value(ingressTimeKey{}).(time.Time)
	return t
}

// arrived records the ingress time unless an outer SDK handler already did.
func arrived(r *http.Request) *http.Request {
	if !IngressTime(r.Context()).IsZero() {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), ingressTimeKey{}, time.Now()))
}
