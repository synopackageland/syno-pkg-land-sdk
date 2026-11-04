package idgateway

import "net/http"

type ingressSchemeKey struct{}

const nativeSchemeHeader = "X-Synopkgland-Scheme"

// Only nativeIngressHandler, after authenticating DSM's private socket hop,
// sets this context value. Public listeners and CGI never trust scheme headers.
// Keep r.TLS intact: it describes the internal connection, not the browser.
func requestScheme(r *http.Request) string {
	if scheme, ok := r.Context().Value(ingressSchemeKey{}).(string); ok {
		return scheme
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func requestSecure(r *http.Request) bool { return requestScheme(r) == "https" }

// A browser cannot overwrite an existing Secure cookie from HTTP. Separate
// ingress HTTP sessions and bindings so a previous HTTPS visit cannot wedge
// sign-in, and HTTPS never reads a cookie issued by the HTTP entry.
func (f *Front) sessionCookie(r *http.Request) string {
	if f.cfg.IngressPath != "" && !requestSecure(r) {
		return f.cookie + "_http"
	}
	return f.cookie
}
