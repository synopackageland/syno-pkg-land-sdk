package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const authenticateCGI = "/usr/syno/synoman/webman/modules/authenticate.cgi"

// Verify checks the current session in DSM's native authenticator, with the
// browser's address. Replaying it to entry.cgi over loopback changes its source
// address: with DSM's IP binding enabled this invalidates the browser session.
// Keep these carriers aligned with dsm.sessionEnv in the parent SDK module.
func (d *liveDSM) Verify(ctx context.Context, cookie, token string) (string, error) {
	addr, ok := clientAddress(ctx)
	ip, err := netip.ParseAddr(addr)
	id := &http.Cookie{Name: "id", Value: cookie}
	if !ok || err != nil || ip.Zone() != "" || d.session == nil || cookie == "" || len(cookie) > 4096 || id.Valid() != nil || token == "" || len(token) > 4096 || strings.ContainsFunc(token, unicode.IsControl) {
		return "", errors.New("session")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := d.session(ctx, authenticateCGI, []string{
		"REQUEST_METHOD=GET",
		"HTTP_COOKIE=" + id.String(),
		"HTTP_X_SYNO_TOKEN=" + token,
		"QUERY_STRING=SynoToken=" + url.QueryEscape(token),
		"REMOTE_ADDR=" + ip.Unmap().String(),
	})
	if err != nil || len(out) > 1026 {
		return "", errors.New("session")
	}
	name := strings.TrimSuffix(strings.TrimSuffix(string(out), "\n"), "\r")
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) || strings.TrimSpace(name) != name || strings.ContainsAny(name, "<>/") || strings.ContainsFunc(name, unicode.IsControl) {
		return "", errors.New("session")
	}
	return name, nil
}

// No credentials in argv, inherited environment, stderr or errors. The command
// is deadline-bound, including descendants that keep its output pipe open.
func runSessionCGI(ctx context.Context, path string, env []string) ([]byte, error) {
	cmd := command(ctx, path, nil)
	cmd.Env = env
	out := &sessionOutput{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, errors.New("session")
	}
	return out.data, nil
}

type sessionOutput struct{ data []byte }

func (b *sessionOutput) Write(p []byte) (int, error) {
	if len(p) > 1026-len(b.data) {
		return 0, errors.New("session output limit")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
