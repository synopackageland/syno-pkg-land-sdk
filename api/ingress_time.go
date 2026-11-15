package api

import (
	"context"
	"net"
	"net/http"
	"time"
)

type ingressTimeKey struct{}
type acceptTimeKey struct{}

// IngressTime is when the request reached the SDK: the outermost SDK handler
// (ServeIngress, WithRequestDeadline, WithObserver, WithDescription or
// Server) records it before it reads the body, checks the origin or
// authenticates the session. Handlers and Authenticators read it from the
// request context, for example to measure a response-time budget. It is the
// zero time for a context that did not pass through an SDK handler, such as
// one a test builds. Under ServeIngress it is when net/http finished reading
// the request header; AcceptTime is when the connection arrived.
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

// AcceptTime is when ServeIngress accepted the request's connection from
// nginx, before net/http read the request header; IngressTime minus
// AcceptTime is the wait outside the App's handler. nginx opens one
// connection per proxied request (the CLI declares no upstream keepalive), so
// it is the request's own arrival; on a reused connection it would be the
// first request's. It is the zero time outside ServeIngress, as under CGI.
func AcceptTime(ctx context.Context) time.Time {
	if ctx == nil {
		return time.Time{}
	}
	t, _ := ctx.Value(acceptTimeKey{}).(time.Time)
	return t
}

func accepted(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, acceptTimeKey{}, time.Now())
}
