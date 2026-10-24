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
func Canonical(raw string) (string, error) {
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
	if u.Port() != "" && u.Port() != "443" {
		return "", invalid
	}
	if !strings.EqualFold(u.Host, host) && !strings.EqualFold(u.Host, host+":443") {
		return "", invalid
	}
	return "https://" + host, nil
}
