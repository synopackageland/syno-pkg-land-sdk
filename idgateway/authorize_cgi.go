package idgateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// ReturnPath is where the authorize CGI sends the browser back to the gateway.
const ReturnPath = "/_sso/dsm-return"

// AssertionTTL bounds the CGI-to-gateway hop.
const AssertionTTL = time.Minute

// SessionVerifier checks a DSM session; dsm.Authenticator implements it.
type SessionVerifier interface {
	Authenticate(*http.Request) (api.Identity, error)
}

// Accounts resolves a verified DSM account name to its uid; Directory implements it.
type Accounts interface {
	UID(ctx context.Context, name string) (int, error)
}

// AuthorizeCGI runs on the DSM origin (/webman/3rdparty/<package>/...). A GET
// answers a bootstrap page that reads the SynoToken same-origin and posts it
// back; the POST verifies cookie, SynoToken and client address with DSM,
// resolves the account's uid and returns the browser to the gateway with a
// sealed Assertion or an OAuth-style error.
type AuthorizeCGI struct {
	External     func() External
	AppHTTPSPort int
	// LoginApp is the CLI-generated DSM App instance used to resume a cold login.
	LoginApp string
	Key      []byte
	AppPorts []int
	Verifier SessionVerifier
	Accounts Accounts
	Now      func() time.Time
}

var txnPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (h *AuthorizeCGI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	external := h.external()
	var prepared bool
	r, prepared = h.prepareResume(w, r, external)
	if !prepared {
		return
	}
	txn := r.URL.Query().Get("txn")
	ret, ok := h.returnURLWithExternal(r, external)
	if !ok || !txnPattern.MatchString(txn) {
		http.Error(w, "invalid sign-in request", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.bootstrap(w, r, ret)
	case http.MethodPost:
		if origin := r.Header.Get("Origin"); origin != "" && origin != requestOrigin(r) {
			http.Error(w, "cross-origin sign-in request", http.StatusForbidden)
			return
		}
		h.verify(w, r, ret, txn, external)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// returnURL accepts only the gateway's return path on one of its ports, on the
// hostname the browser used for DSM: DSM's cookie never reaches another host.
func (h *AuthorizeCGI) returnURL(r *http.Request) (*url.URL, bool) {
	return h.returnURLWithExternal(r, h.external())
}

func (h *AuthorizeCGI) returnURLWithExternal(r *http.Request, e External) (*url.URL, bool) {
	ret, err := url.Parse(r.URL.Query().Get("return"))
	if err != nil || (ret.Scheme != "http" && ret.Scheme != "https") || !validReturnPath(ret) {
		return nil, false
	}
	if e.Index(ret.Scheme+"://"+ret.Host) >= 0 {
		return ret, true
	}
	port, err := strconv.Atoi(ret.Port())
	if err != nil || !slices.Contains(h.AppPorts, port) || !strings.EqualFold(ret.Hostname(), hostname(r.Host)) {
		return nil, false
	}
	return ret, true
}

func (h *AuthorizeCGI) bootstrap(w http.ResponseWriter, r *http.Request, ret *url.URL) {
	nonce := randomID(16)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; script-src 'nonce-%s'; connect-src 'self'; form-action 'self' %s://%s; frame-ancestors 'none'", nonce, ret.Scheme, ret.Host))
	// Keep the normalized resume query, and carry the inherited App fragment
	// in the form's navigation URL only. It must never become a form field or
	// query parameter: the browser retains it across the return redirects.
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Signing in</title>
<form id="f" method="post" action="?%s"><input type="hidden" name="SynoToken" id="t"></form>
<script nonce="%s">var f=document.getElementById('f');f.action+=location.hash;fetch('/webman/login.cgi?enable_syno_token=yes',{credentials:'same-origin'}).then(function(r){return r.json()}).then(function(j){document.getElementById('t').value=j.SynoToken||''}).catch(function(){}).then(function(){f.submit()})</script>`,
		html.EscapeString(r.URL.RawQuery), nonce)
}

func (h *AuthorizeCGI) verify(w http.ResponseWriter, r *http.Request, ret *url.URL, txn string, external External) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	token := ""
	if r.ParseForm() == nil {
		token = r.PostForm.Get("SynoToken")
	}
	session := r.Clone(r.Context())
	session.Header.Set("X-SYNO-TOKEN", token)
	q := url.Values{"txn": {txn}}
	id, err := h.Verifier.Authenticate(session)
	var uid int
	if err == nil {
		uid, err = h.Accounts.UID(r.Context(), id.User)
	}
	switch {
	case errors.Is(err, api.ErrAppPrivilegeDenied):
		q.Set("error", "access_denied")
		q.Set("error_reason", "app_privilege_denied")
	case errors.Is(err, api.ErrAdminRequired) || errors.Is(err, ErrGroupRequired):
		q.Set("error", "access_denied")
	case errors.Is(err, api.ErrNoSession) || errors.Is(err, ErrNoAccount):
		if namePattern.MatchString(h.LoginApp) && r.URL.Query().Get("login_attempt") != "1" {
			resume := r.URL.Query().Get("resume")
			if resume == "" {
				if index := external.Index(ret.Scheme + "://" + ret.Host); index >= 0 {
					resume = externalResume(txn, index, external)
				} else {
					resume = txn + ":" + ret.Port()
				}
			}
			param, _ := json.Marshal(map[string]string{"ssoResume": resume})
			launch := url.Values{"launchApp": {"SYNO.SDS." + h.LoginApp + ".Instance"}, "launchParam": {string(param)}}
			http.Redirect(w, r, "/?"+launch.Encode(), http.StatusFound)
			return
		}
		q.Set("error", "login_required")
	case err != nil:
		q.Set("error", "temporarily_unavailable")
	default:
		sealed, err := SealAssertion(h.Key, Assertion{Txn: txn, Origin: ret.Scheme + "://" + ret.Host, Subject: Subject{UID: uid, Name: id.User, Admin: id.Admin}, Expires: h.Now().Add(AssertionTTL).Unix()})
		if err != nil {
			q.Set("error", "server_error")
		} else {
			q.Set("assertion", sealed)
		}
	}
	ret.RawQuery = q.Encode()
	http.Redirect(w, r, ret.String(), http.StatusFound)
}

func requestOrigin(r *http.Request) string {
	if r.TLS != nil {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

func hostname(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.Trim(hostport, "[]")
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
