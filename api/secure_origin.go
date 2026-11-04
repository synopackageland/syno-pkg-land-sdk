package api

import (
	"net/http"
	"net/url"
)

// RequireSecureOrigin is an opt-in check for Apps that need an HTTPS browser
// origin. It does not authenticate a session or replace CheckOrigin. Use it
// in App middleware; ordinary HTTP-capable Apps need no additional guard.
// Origin takes precedence over Referer. Proxy/TLS metadata cannot establish
// which page sent the request, and missing or opaque sources fail closed.
func RequireSecureOrigin(r *http.Request) *Error {
	source := r.Header.Get("Origin")
	if source == "" {
		source = r.Header.Get("Referer")
	}
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return &Error{"https_required", "Open this App using HTTPS"}
	}
	return nil
}
