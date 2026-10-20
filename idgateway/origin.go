package idgateway

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Origin/Referer are browser-controlled evidence, not credentials. SameSite is
// additional protection: it neither isolates ports nor makes same-site trusted.
func sameBrowserOrigin(r *http.Request, expected string) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	values := r.Header.Values("Origin")
	if len(values) > 1 {
		return false
	}
	if len(values) == 1 {
		return normalizedOrigin(values[0], false) == expectedOrigin(expected)
	}
	values = r.Header.Values("Referer")
	return len(values) == 1 && normalizedOrigin(values[0], true) == expectedOrigin(expected)
}

func expectedOrigin(raw string) string { return normalizedOrigin(raw, false) }

func normalizedOrigin(raw string, referer bool) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || strings.ContainsAny(u.Host, "\\\r\n \t") {
		return ""
	}
	if !referer && (u.Path != "" || u.RawQuery != "" || u.Fragment != "") {
		return ""
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func validRequestAuthority(r *http.Request) bool {
	return normalizedOrigin(requestOrigin(r), false) != ""
}

func safeProxyMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}
func isUpgrade(r *http.Request) bool { return r.Header.Get("Upgrade") != "" }
