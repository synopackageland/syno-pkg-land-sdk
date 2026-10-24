// Package externalorigin validates literal public origins without DNS or HTTP.
package externalorigin

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var label = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// Canonical accepts an HTTPS DNS origin, folding case and dropping :443.
// Paths (including /), IP literals, credentials and non-default ports are refused.
func Canonical(raw string) (string, error) { return canonical(raw, false) }

// CanonicalWithPort is Canonical that also accepts an explicit port 1-65535,
// as a private network address such as https://nas.example.ts.net:5001 uses.
// :443 is still dropped, so both forms compare equal.
func CanonicalWithPort(raw string) (string, error) { return canonical(raw, true) }

func canonical(raw string, anyPort bool) (string, error) {
	invalid := errors.New("invalid external origin: expected https://<DNS hostname>")
	if len(raw) > 2048 || !strings.HasPrefix(raw, "https://") || strings.ContainsAny(strings.TrimPrefix(raw, "https://"), "/?#\\ \t\r\n") {
		return "", invalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", invalid
	}
	host := strings.ToLower(u.Hostname())
	if len(host) > 253 || !strings.Contains(host, ".") || net.ParseIP(host) != nil || strings.Trim(host, "0123456789.") == "" {
		return "", invalid
	}
	for _, part := range strings.Split(host, ".") {
		if !label.MatchString(part) {
			return "", invalid
		}
	}
	port := u.Port()
	if port == "443" {
		port = ""
	}
	if port != "" && (!anyPort || !validPort(port)) {
		return "", invalid
	}
	if !strings.EqualFold(u.Host, host) && (u.Port() == "" || !strings.EqualFold(u.Host, host+":"+u.Port())) {
		return "", invalid
	}
	if port != "" {
		return "https://" + host + ":" + port, nil
	}
	return "https://" + host, nil
}

func validPort(port string) bool {
	if port == "" || len(port) > 5 || port[0] == '0' {
		return false
	}
	n := 0
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n <= 65535
}
