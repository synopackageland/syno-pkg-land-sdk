// Package dsm verifies the DSM login behind a package CGI request.
//
// DSM runs a package's CGI as the package account and forwards the browser's
// cookies. This package hands only the session carriers (cookie, SynoToken,
// client address) to DSM's own authenticate.cgi, which prints the signed-in
// user name. DSM's native SDK then resolves administrator membership for
// local, AD and LDAP accounts; no group-name comparison lives in this package.
package dsm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// AuthenticateCGI is DSM's session check for third-party CGIs.
const AuthenticateCGI = "/usr/syno/synoman/webman/modules/authenticate.cgi"

// Authenticator implements api.Authenticator against DSM.
type Authenticator struct {
	Timeout         time.Duration
	run             func(ctx context.Context, path string, env []string) ([]byte, error)
	isAdministrator func(context.Context, string) (bool, error)
}

func NewAuthenticator() *Authenticator {
	return &Authenticator{Timeout: 10 * time.Second, run: runCGI, isAdministrator: IsAdministrator}
}

// The name comes only from authenticate.cgi. Preserve directory-qualified and
// Unicode principals verbatim; controls, multiline and unbounded names refuse.
func validPrincipal(name string) bool {
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) || strings.TrimSpace(name) != name || strings.ContainsAny(name, "<>/") {
		return false
	}
	for _, ch := range name {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

func (a *Authenticator) Authenticate(r *http.Request) (api.Identity, error) {
	cookie := r.Header.Get("Cookie")
	if cookie == "" {
		return api.Identity{}, api.ErrNoSession
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.Timeout)
	defer cancel()
	out, err := a.run(ctx, AuthenticateCGI, sessionEnv(r, cookie))
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		return api.Identity{}, api.ErrNoSession
	}
	if err != nil {
		return api.Identity{}, fmt.Errorf("run %s: %w", AuthenticateCGI, err)
	}
	user := strings.TrimSuffix(strings.TrimSuffix(string(out), "\n"), "\r")
	if !validPrincipal(user) {
		return api.Identity{}, api.ErrNoSession
	}
	admin, err := a.isAdministrator(ctx, user)
	if err != nil {
		return api.Identity{}, err
	}
	return api.Identity{User: user, Admin: admin}, nil
}

// sessionEnv is the complete environment authenticate.cgi sees: nothing from
// this process and no request header besides the session carriers.
func sessionEnv(r *http.Request, cookie string) []string {
	token := r.Header.Get("X-SYNO-TOKEN")
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return []string{
		"REQUEST_METHOD=GET",
		"HTTP_COOKIE=" + cookie,
		"HTTP_X_SYNO_TOKEN=" + token,
		"QUERY_STRING=SynoToken=" + url.QueryEscape(token),
		"REMOTE_ADDR=" + host,
	}
}
