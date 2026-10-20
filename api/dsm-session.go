package api

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"unicode"
)

// DSMSession forwards only the current request's DSM authentication material
// and the browser's address (REMOTE_ADDR).
// Identical X-SYNO-TOKEN and SynoToken aliases are allowed; conflicting values
// or multiple values for either header are refused. A session cookie without
// a token is credential_not_ready, anything else session_required. Tokens are never accepted
// from request params or environment variables.
func DSMSession(r *http.Request) (map[string]string, error) {
	if r == nil {
		return nil, sessionRequired()
	}
	// Inspect raw headers before net/http can silently discard malformed values.
	ids := 0
	for _, line := range r.Header.Values("Cookie") {
		if strings.ContainsFunc(line, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, sessionRequired()
		}
		for _, part := range strings.Split(line, ";") {
			name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
			if strings.TrimSpace(name) == "id" {
				ids++
			}
		}
	}
	if ids != 1 {
		return nil, sessionRequired()
	}
	cookies := r.CookiesNamed("id")
	if len(cookies) != 1 || cookies[0].Value == "" || len(cookies[0].Value) > 4096 || (strings.Contains(cookies[0].Value, ";") || strings.ContainsFunc(cookies[0].Value, func(r rune) bool { return r < 0x20 || r == 0x7f })) {
		return nil, sessionRequired()
	}
	token := ""
	for _, name := range []string{"X-SYNO-TOKEN", "SynoToken"} {
		values := r.Header.Values(name)
		if len(values) > 1 {
			return nil, sessionRequired()
		}
		if len(values) == 1 && values[0] != "" {
			if token != "" && token != values[0] {
				return nil, sessionRequired()
			}
			token = values[0]
		}
	}
	// A signed-in browser whose page has no SynoToken (a DSM session restored
	// without a password sign-in, an Application Portal page) is told so.
	if token == "" {
		return nil, CredentialNotReady()
	}
	if len(token) > 4096 || strings.ContainsFunc(token, unicode.IsControl) {
		return nil, sessionRequired()
	}
	auth := map[string]string{"dsm_cookie": cookies[0].Value, "dsm_synotoken": token}
	// The executor checks App Privileges against this address and refuses
	// without it, so a missing one is left for it to refuse.
	if addr, err := ClientAddress(r); err == nil {
		auth["remote_addr"] = addr
	}
	return auth, nil
}

// CredentialNotReady is DSMSession's refusal of a session cookie that came
// without a SynoToken; its message is spec/bridge/v1/errors.json's en text.
func CredentialNotReady() *Error {
	return &Error{Code: "credential_not_ready", Message: "DSM did not give this page its sign-in token. Sign in to DSM again with your password, then open the App from the DSM desktop."}
}

func sessionRequired() *Error {
	return &Error{Code: "session_required", Message: "DSM session cookie and token are required"}
}

// ClientAddress is the browser's address as DSM's web server reported it
// (REMOTE_ADDR); DSM's IP rules are evaluated against it.
func ClientAddress(r *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", errors.New("client address unavailable")
	}
	return ip.String(), nil
}
