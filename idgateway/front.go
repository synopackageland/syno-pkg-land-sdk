package idgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeingress"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mode is how the gateway delivers the signed-in DSM user to the app.
type Mode string

const (
	// ModeNone preserves the app's native login without injecting DSM identity.
	ModeNone Mode = "none"
	// ModeOIDC: the app signs in with the embedded OpenID Connect provider.
	ModeOIDC Mode = "oidc"
	// ModeHeader: the gateway injects trusted identity headers.
	ModeHeader Mode = "header"
	// ModeToken: a TokenSource supplies the app's own session credentials.
	ModeToken Mode = "token"
)

// Gateway lifetimes.
const (
	TxnTTL            = 5 * time.Minute
	DefaultSessionTTL = time.Hour
)

// DSMPorts are DSM's own HTTP and HTTPS ports, where the authorize CGI runs.
type DSMPorts struct{ HTTP, HTTPS int }

// HeaderNames are the trusted identity headers in header mode. User is
// required; empty names are not sent.
type HeaderNames struct{ User, Email, Name, Groups, UID string }

func (h HeaderNames) all() []string {
	var out []string
	for _, n := range []string{h.User, h.Email, h.Name, h.Groups, h.UID} {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// JSONRewrite names a JSON response whose string Field may hold the
// provider's authorization URL, which the gateway rewrites to the app origin.
type JSONRewrite struct{ Method, Path, Field string }

// TokenSource supplies the app's own credentials for subject in token mode,
// as request headers that replace the client's.
type TokenSource interface {
	Credentials(ctx context.Context, subject Subject) (http.Header, error)
}

// Names resolves a uid to its current DSM account name; Directory implements it.
// Implementations must honor context cancellation to release pending lookups.
type Names interface {
	Name(ctx context.Context, uid int) (string, error)
}

// FrontConfig configures the gateway on the app's ports.
type FrontConfig struct {
	// SecureContext is empty (HTTP allowed) or required (HTTPS guidance).
	SecureContext string
	// DSMHTTPSPort is a declared DSM HTTPS port, with zero meaning unknown.
	// Unlike DSMPorts, this value must never include a fallback port.
	DSMHTTPSPort int
	IngressPath  string
	// External resolves the current public-origin policy; nil preserves local
	// behavior. ExternalFresh, when set, rechecks it for requests on a public
	// origin; see ExternalReader.Fresh.
	External      func() External
	ExternalFresh func() External
	// SingleOrigin redirects browser navigations on other hosts to the primary
	// external origin while one is set; see ExternalConfig.SingleOrigin.
	SingleOrigin   bool
	Ready          func(context.Context) bool
	ComposeIngress *ComposeIngress
	Access         string
	Entry          string
	// UpstreamAuthPaths delegates these fixed path prefixes to upstream auth.
	// It never changes SDK RPC authorization.
	UpstreamAuthPaths     []string
	UpstreamAccess        string
	Groups                []string
	Membership            GroupMembership
	ComposeBlocked        bool
	MaxUploadBytes        int64
	ResponseHeaderTimeout time.Duration
	Mode                  Mode
	// Upstream is the loopback or package-derived Compose bridge base URL.
	Upstream string
	// Key is shared with the authorize CGI.
	Key []byte
	// AuthorizeCGI is the CGI's path on the DSM origin.
	AuthorizeCGI string
	DSMPorts     DSMPorts
	// Package scopes the gateway's cookie names.
	Package     string
	EmailDomain string
	// Passthrough are path prefixes the app authenticates itself (public
	// shares, WebDAV, native clients); the gateway forwards them without
	// identity.
	Passthrough         []string
	OptionalPassthrough map[string]OptionalPassthroughRule
	StateDir            string
	// Redirects answers GET of an exact path with a redirect, to start the
	// app's own sign-in.
	Redirects map[string]string

	// oidc
	Browser         *BrowserConfig
	BrowserRedirect *BrowserRedirectConfig
	PublicListen    Listen
	Provider        *Provider
	Issuer          string
	RewriteJSON     []JSONRewrite

	// header and token
	Headers    HeaderNames
	Roles      Roles
	Tokens     TokenSource
	Accounts   Names
	SessionTTL time.Duration

	// SignedIn, when set, sees each verified sign-in.
	SignedIn func(Subject)

	Now          func() time.Time
	appPrivilege func(context.Context, string, string, string) (bool, error)
}

type txn struct {
	recovery bool
	ip       string
	oidc     bool
	req      AuthRequest
	returnTo string
	origin   string
	browser  string
	expires  time.Time
}

type session struct {
	subject Subject
	ip      string
	expires time.Time
}

// Front is the gateway handler on the app's ports.
type Front struct {
	verificationChecks[struct{}]
	resourceWaiters chan struct{}
	httpWarning     sync.Once
	optional        *optionalPassthrough
	cfg             FrontConfig
	brand           appBrand
	upstream        *url.URL
	proxy           *httputil.ReverseProxy
	cookie          string

	mu           sync.Mutex // Authenticated sessions only
	txnMu        sync.Mutex // Unauthenticated transactions never take mu
	txns         map[string]txn
	txnClients   map[string]int
	nextTxnSweep time.Time
	sessions     map[string]session
}

type ctxKey int

const (
	subjectKey ctxKey = iota
	originKey
	upgradeKey
)

// NewFront validates cfg and builds the gateway handler.
func NewFront(cfg FrontConfig) (*Front, error) {
	if err := validateSecureContext(cfg.SecureContext); err != nil {
		return nil, err
	}
	if err := (Config{Entry: cfg.Entry, Access: cfg.Access, UpstreamAccess: cfg.UpstreamAccess, Groups: cfg.Groups, ComposeBlocked: cfg.ComposeBlocked, UpstreamAuthPaths: cfg.UpstreamAuthPaths, Passthrough: cfg.Passthrough, OptionalPassthrough: cfg.OptionalPassthrough}).checkEntryPolicy(); err != nil {
		return nil, err
	}
	if err := validateUpstreamAuth(cfg.UpstreamAuthPaths, cfg.Passthrough, cfg.OptionalPassthrough); err != nil {
		return nil, err
	}
	if err := validateEntry(cfg.Entry, cfg.Access, cfg.UpstreamAccess, len(cfg.Passthrough), len(cfg.OptionalPassthrough), cfg.UpstreamAuthPaths); err != nil {
		return nil, err
	}
	if cfg.Entry == "admin-only" {
		cfg.Access = "dsm-admins"
	}
	if cfg.appPrivilege == nil {
		cfg.appPrivilege = dsm.AppPrivilege
	}
	if err := cfg.BrowserRedirect.validate(); err != nil {
		return nil, err
	}
	if cfg.BrowserRedirect != nil && (cfg.Mode != ModeOIDC || cfg.PublicListen.HTTP == 0 && cfg.PublicListen.HTTPS == 0 || !validPort(cfg.PublicListen.HTTP, true) || !validPort(cfg.PublicListen.HTTPS, true)) {
		return nil, errors.New("oidc.browser_redirect requires oidc mode and declared public listen ports")
	}
	if err := validateUpstreamAccess(cfg.Mode, cfg.Access, cfg.UpstreamAccess, cfg.ComposeBlocked, len(cfg.OptionalPassthrough) > 0); err != nil {
		return nil, err
	}
	if cfg.Browser != nil {
		if cfg.Mode != ModeOIDC {
			return nil, errors.New("browser bridge requires oidc mode")
		}
		if err := cfg.Browser.validate(); err != nil {
			return nil, err
		}
	}
	if err := validateOptional(cfg.OptionalPassthrough, cfg.Passthrough); err != nil {
		return nil, err
	}
	if len(cfg.OptionalPassthrough) > 0 && !filepath.IsAbs(cfg.StateDir) {
		return nil, errors.New("optional passthrough needs an absolute state directory")
	}
	up, err := url.Parse(cfg.Upstream)
	valid := err == nil && up.Scheme == "http" && (up.Path == "" || up.Path == "/") && up.User == nil && up.RawQuery == "" && up.Fragment == "" && net.ParseIP(up.Hostname()) != nil
	if valid && cfg.ComposeIngress != nil {
		a, e := composeingress.Allocate(cfg.Package, cfg.ComposeIngress.Candidate)
		port, e2 := strconv.Atoi(up.Port())
		valid = e == nil && e2 == nil && port > 0 && port <= 65535 && up.Hostname() == a.Address
	} else if valid {
		valid = net.ParseIP(up.Hostname()).IsLoopback()
	}
	if !valid {
		return nil, fmt.Errorf("upstream %q must be an http loopback or exact package Compose ingress address", cfg.Upstream)
	}
	if len(cfg.Key) < MinKeySize || !strings.HasPrefix(cfg.AuthorizeCGI, "/webman/3rdparty/") || cfg.Package == "" {
		return nil, errors.New("gateway needs a key, the authorize CGI path and the package name")
	}
	for _, p := range cfg.Passthrough {
		if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "/_sso") || p == "/" {
			return nil, fmt.Errorf("passthrough %q must be an absolute path prefix outside /_sso", p)
		}
	}
	switch cfg.Mode {
	case ModeNone:
		if cfg.Accounts == nil {
			return nil, errors.New("none mode needs an account directory")
		}
	case ModeOIDC:
		if cfg.Provider == nil || cfg.Issuer == "" {
			return nil, errors.New("oidc mode needs the provider and its issuer")
		}
	case ModeHeader:
		if cfg.Headers.User == "" || cfg.Accounts == nil {
			return nil, errors.New("header mode needs a user header and an account directory")
		}
	case ModeToken:
		if cfg.Tokens == nil || cfg.Accounts == nil {
			return nil, errors.New("token mode needs a token source and an account directory")
		}
	default:
		return nil, fmt.Errorf("unknown mode %q", cfg.Mode)
	}
	if cfg.EmailDomain == "" {
		cfg.EmailDomain = "nas.invalid"
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = DefaultSessionTTL
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	cfg.Roles = cfg.Roles.orDefault()
	if err := validateIngressPath(cfg.Package, cfg.IngressPath); err != nil {
		return nil, err
	}
	cfg = scopeFront(cfg)
	f := &Front{resourceWaiters: make(chan struct{}, resourceWaitLimit), brand: readAppBrand("/var/packages", cfg.Package), cfg: cfg, upstream: up, cookie: "synopkgland_sso_" + cfg.Package, txns: map[string]txn{}, sessions: map[string]session{}}
	if cfg.Provider != nil && cfg.Entry != "" {
		cfg.Provider.admit = f.admitClient
	}
	if len(cfg.OptionalPassthrough) > 0 {
		f.optional = &optionalPassthrough{dir: cfg.StateDir, rules: cfg.OptionalPassthrough}
		f.optional.reload()
	}
	if cfg.Access != "" && cfg.Access != "public" && cfg.Access != "dsm-users" && cfg.Access != "dsm-admins" && cfg.Access != "dsm-groups" {
		return nil, errors.New("unsupported gateway access")
	}
	if cfg.Access == "public" && cfg.Mode != ModeNone {
		return nil, errors.New("public access requires mode none")
	}
	if cfg.ComposeBlocked && cfg.Access == "public" {
		return nil, errors.New("Share settings require DSM administrator admission")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: runtimebudget.Operation, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = cfg.ResponseHeaderTimeout
	transport.TLSHandshakeTimeout = runtimebudget.Operation
	f.proxy = &httputil.ReverseProxy{Rewrite: f.rewrite, ModifyResponse: f.modifyProxyResponse, Transport: &boundedProxyTransport{base: transport, headers: make(chan struct{}, resourceWaitLimit)}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, errUpstreamBusy) {
			writeStarting(w, r)
			return
		}
		var admission *resourceAccessError
		if errors.As(err, &admission) {
			w.Header().Set("X-Synopkgland-Error-Stage", "admission")
			f.writeFailure(w, r, admission.code, "", false)
			return
		}
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			http.Error(w, "upload exceeds max_upload_bytes", http.StatusRequestEntityTooLarge)
			return
		}
		// A refused or timed-out connection is how a restarting or stalled App
		// looks from here; anything else is still a gateway error.
		var op *net.OpError
		if errors.As(err, &op) {
			writeStarting(w, r)
			return
		}
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}}
	return f, nil
}

func (f *Front) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !requestSecure(r) {
		f.httpWarning.Do(func() { log.Print("idgateway: HTTP is unencrypted; use HTTPS when available") })
	}
	if f.cfg.IngressPath != "" {
		if !strings.HasPrefix(r.URL.Path, f.cfg.IngressPath) {
			http.NotFound(w, r)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ingressPathKey{}, f.cfg.IngressPath))
	}
	if r.URL.Path == f.route(PingPath) {
		servePackagePing(w, r, f.cfg.Package)
		return
	}
	if !cleanPath(r.URL.Path) || (f.optional != nil || len(f.cfg.UpstreamAuthPaths) > 0) && !cleanOptionalPath(r) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if f.cfg.SecureContext == "required" && !requestSecure(r) {
		if !validRequestAuthority(r) {
			http.Error(w, "invalid authority", http.StatusBadRequest)
			return
		}
		f.writeHTTPSRequired(w, r)
		return
	}
	if f.cfg.External != nil {
		r = withExternal(r, f.external(r))
	}
	optional := false
	if f.optional != nil {
		id, scope, enabled := f.optional.admit(r.URL.Path)
		if id != "" {
			if !enabled || f.cfg.ComposeBlocked {
				http.Error(w, "client access disabled", http.StatusForbidden)
				return
			}
			if !f.optional.destinationAllowed(r, id) {
				http.Error(w, "invalid destination", http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithCancel(r.Context())
			stop := context.AfterFunc(scope, cancel)
			if scope.Err() != nil {
				cancel()
			}
			defer stop()
			defer cancel()
			r = r.WithContext(ctx)
			optional = true
		}
	}
	origin := requestOrigin(r)
	upstreamAuth := !f.cfg.ComposeBlocked && f.upstreamAuthenticated(r)
	if external := f.external(r); external.Index(origin) >= 0 {
		origin = external.Accept[external.Index(origin)]
	}
	if !validRequestAuthority(r) || !f.browserAuthority(r) {
		http.Error(w, "invalid authority", http.StatusBadRequest)
		return
	}
	if target := f.canonicalNavigation(r, origin); !upstreamAuth && target != "" {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	// Explicit app-authenticated passthrough also serves native clients without
	// browser origin headers. It never receives gateway identity or cookies.
	browserEvidence := !upstreamAuth && (!(optional || f.passthrough(r.URL.Path)) || r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" || isUpgrade(r))
	// Public App routes own authentication, CSRF and native-client/WebSocket
	// policy. Reserved issuer routes keep the gateway's existing protections.
	if f.providerOnly() && r.URL.Path != f.route("/_sso") && !strings.HasPrefix(r.URL.Path, f.route("/_sso/")) {
		browserEvidence = false
	}
	if browserEvidence && (!safeProxyMethod(r.Method) || isUpgrade(r)) && !sameBrowserOrigin(r, origin) {
		http.Error(w, "cross-origin request", http.StatusForbidden)
		return
	}
	if isUpgrade(r) && !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "unsupported upgrade", http.StatusNotImplemented)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), originKey, origin))
	switch {
	case r.URL.Path == f.route("/_sso/browser.js") && f.cfg.Browser != nil:
		f.serveBrowser(w, r)
		return
	case r.URL.Path == f.route("/_sso/authorize") && f.cfg.Mode == ModeOIDC:
		f.authorize(w, r, origin)
		return
	case r.URL.Path == f.route(ReturnPath):
		f.dsmReturn(w, r, origin)
		return
	case r.URL.Path == f.route("/_sso/logout") && f.cfg.Mode != ModeOIDC:
		f.logout(w, r, origin)
		return
	case r.URL.Path == f.route("/_sso") || strings.HasPrefix(r.URL.Path, f.route("/_sso/")):
		http.NotFound(w, r)
		return
	}
	if upstreamAuth {
		f.serveProxy(w, r)
		return
	}
	if target, ok := f.cfg.Redirects[r.URL.Path]; ok && r.Method == http.MethodGet {
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	if f.cfg.Entry == "" && !f.cfg.ComposeBlocked && (f.providerOnly() || optional || f.cfg.Access == "public" || f.cfg.Mode == ModeOIDC && f.cfg.Access == "" || f.passthrough(r.URL.Path)) {
		f.serveProxy(w, r)
		return
	}
	verifyCtx, stopVerify := context.WithTimeout(r.Context(), runtimebudget.Operation)
	defer stopVerify()
	verifiedRequest := r.WithContext(verifyCtx)
	s, sessionErr := f.lookupSession(w, verifiedRequest)
	if sessionErr != nil && !errors.Is(sessionErr, errSessionChanged) {
		w.Header().Set("X-Synopkgland-Error-Stage", "admission")
		f.writeFailure(w, r, "temporarily_unavailable", "", isNavigation(r))
		return
	}
	if sessionErr != nil {
		if isNavigation(r) {
			f.bounce(w, r, txn{returnTo: r.URL.RequestURI()}, origin)
			return
		}
		f.writeFailure(w, r, "login_required", "", false)
		return
	}
	if err := f.admitSession(verifiedRequest); err != nil {
		w.Header().Set("X-Synopkgland-Error-Stage", "admission")
		f.writeFailure(w, r, admissionCode(err), "", isNavigation(r))
		return
	}
	stopVerify()
	if f.cfg.ComposeBlocked {
		f.composeSettings(w, r, s)
		return
	}
	f.serveProxy(w, r.WithContext(context.WithValue(r.Context(), subjectKey, s)))
}

// cleanPath refuses dot segments and backslashes, which an app could resolve
// to a path outside the prefix the gateway matched.
func cleanPath(p string) bool {
	if strings.Contains(p, `\`) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func (f *Front) passthrough(p string) bool {
	for _, prefix := range f.cfg.Passthrough {
		base := strings.TrimSuffix(prefix, "/")
		if p == base || strings.HasPrefix(p, base+"/") {
			return true
		}
	}
	return false
}

func isNavigation(r *http.Request) bool {
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" {
		return mode == "navigate" && r.Method == http.MethodGet
	}
	return r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (f *Front) authorize(w http.ResponseWriter, r *http.Request, origin string) {
	req, err := f.cfg.Provider.Begin(origin, r.URL.Query())
	req.clientIP = remoteIP(r)
	var ae *AuthorizeError
	switch {
	case errors.As(err, &ae):
		http.Redirect(w, r, f.cfg.Provider.Fail(req, ae.Code), http.StatusFound)
	case err != nil:
		http.Error(w, "unknown client or redirect URI", http.StatusBadRequest)
	default:
		f.bounce(w, r, txn{oidc: true, req: req}, origin)
	}
}

// bounce records the transaction and sends the browser to the authorize CGI
// on the DSM origin of the same hostname and scheme.
func (f *Front) bounce(w http.ResponseWriter, r *http.Request, t txn, origin string) {
	target, reason := f.signInTarget(w, r, t, origin, false)
	switch reason {
	case http.StatusServiceUnavailable:
		writeNoDSMOrigin(w, r)
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", "30")
		http.Error(w, "too many sign-in requests", reason)
	default:
		http.Redirect(w, r, target, http.StatusFound)
	}
}

// signInTarget creates a new browser-bound transaction. A user-initiated
// restart goes directly to the DSM launcher; its return still carries the
// one-attempt guard, so even a cancelled login cannot redirect in a loop.
func (f *Front) signInTarget(w http.ResponseWriter, r *http.Request, t txn, origin string, login bool) (string, int) {
	external := f.external(r)
	index := external.Index(origin)
	dsmOrigin := f.dsmOrigin(r)
	if index >= 0 {
		if external.DSM == "" {
			return "", http.StatusServiceUnavailable
		}
		dsmOrigin = external.DSM
	}
	id := randomID(24)
	if t.recovery {
		// The guard survives missing cookies, expired/consumed transactions and
		// gateway restarts. It grants no authority; assertions still bind the ID.
		id = recoveryTxnPrefix + id
	}
	t.origin, t.browser, t.expires = origin, f.browserID(w, r), f.cfg.Now().Add(TxnTTL)
	t.ip = remoteIP(r)
	if !f.addTxn(id, t) {
		return "", http.StatusTooManyRequests
	}
	q := url.Values{"txn": {id}, "return": {origin + f.route(ReturnPath)}}
	appOrigin, _ := url.Parse(origin)
	resume := id + ":" + appOrigin.Port()
	if f.cfg.IngressPath != "" {
		q.Set("resume", id+":p")
	} else if index >= 0 {
		q.Set("resume", externalResume(id, index, external))
	}
	if q.Get("resume") != "" {
		resume = q.Get("resume")
	}
	if login {
		return dsmOrigin + loginLocation(f.cfg.Package, resume), 0
	}
	return dsmOrigin + f.cfg.AuthorizeCGI + "?" + q.Encode(), 0
}

func (f *Front) dsmOrigin(r *http.Request) string {
	if f.cfg.IngressPath != "" {
		return requestOrigin(r)
	}
	if requestSecure(r) {
		return "https://" + net.JoinHostPort(hostname(r.Host), strconv.Itoa(f.cfg.DSMPorts.HTTPS))
	}
	return "http://" + net.JoinHostPort(hostname(r.Host), strconv.Itoa(f.cfg.DSMPorts.HTTP))
}

// browserID binds transactions to the browser that started them, so an
// assertion for one person cannot sign in another person's browser.
func (f *Front) browserID(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(f.sessionCookie(r) + "_b"); err == nil && txnPattern.MatchString(c.Value) {
		return c.Value
	}
	id := randomID(24)
	setCookie(w, r, f.sessionCookie(r)+"_b", id)
	return id
}

func (f *Front) dsmReturn(w http.ResponseWriter, r *http.Request, origin string) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	t, ok := f.takeTxn(r, q.Get("txn"), origin)
	if !ok {
		f.recoverSignIn(w, r, origin, t, "sign_in_expired")
		return
	}
	if q.Get("error") != "" {
		code := q.Get("error")
		if code == "access_denied" && q.Get("error_reason") == "app_privilege_denied" {
			code = "app_privilege_denied"
		}
		f.denied(w, r, t, code)
		return
	}
	a, err := OpenAssertion(f.cfg.Key, q.Get("assertion"), f.cfg.Now())
	if err != nil || a.Txn != q.Get("txn") || a.Origin != origin {
		f.recoverSignIn(w, r, origin, t, "assertion_invalid")
		return
	}
	// Membership may change between CGI verification and assertion redemption.
	if err := f.admit(r, a.Subject); err != nil {
		f.denied(w, r, t, admissionCode(err))
		return
	}
	if f.cfg.SignedIn != nil {
		f.cfg.SignedIn(a.Subject)
	}
	id := randomID(32)
	if !f.addSession(id, session{subject: a.Subject, ip: remoteIP(r), expires: f.cfg.Now().Add(f.cfg.SessionTTL)}) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "too many sign-in sessions", http.StatusTooManyRequests)
		return
	}
	setCookie(w, r, f.sessionCookie(r), id)
	if t.oidc {
		http.Redirect(w, r, f.cfg.Provider.Complete(t.req, a.Subject), http.StatusFound)
		return
	}
	http.Redirect(w, r, localTarget(t.returnTo), http.StatusFound)
}

// takeTxn consumes the transaction only for the browser and origin that
// started it, so a stray or hostile request cannot burn someone's sign-in.
func (f *Front) takeTxn(r *http.Request, id, origin string) (txn, bool) {
	browser, err := r.Cookie(f.sessionCookie(r) + "_b")
	f.txnMu.Lock()
	defer f.txnMu.Unlock()
	t, ok := f.txns[id]
	if !ok || err != nil || browser.Value != t.browser || t.origin != origin {
		// Never disclose or resume another browser's target or OIDC request.
		return txn{recovery: strings.HasPrefix(id, recoveryTxnPrefix)}, false
	}
	f.deleteTxn(id, t)
	return t, !f.cfg.Now().After(t.expires)
}

func localTarget(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return "/"
	}
	u, err := url.ParseRequestURI(p)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "/"
	}
	path, err := url.PathUnescape(u.EscapedPath())
	if err != nil || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\\r\n") {
		return "/"
	}
	return p
}

func (f *Front) denied(w http.ResponseWriter, r *http.Request, t txn, code string) {
	switch code {
	case "access_denied", "app_privilege_denied", "login_required", "temporarily_unavailable":
	default:
		code = "server_error"
	}
	if t.oidc && t.req.Prompt == "none" {
		if code == "app_privilege_denied" {
			code = "access_denied"
		}
		http.Redirect(w, r, f.cfg.Provider.Fail(t.req, code), http.StatusFound)
		return
	}
	retry := f.route("/")
	if !t.oidc {
		retry = localTarget(t.returnTo)
	}
	f.writeFailure(w, r, code, retry, true, t)
}

func (f *Front) logout(w http.ResponseWriter, r *http.Request, origin string) {
	if r.Method != http.MethodPost || r.Header.Get("Origin") != origin {
		http.Error(w, "sign out with a same-origin POST", http.StatusForbidden)
		return
	}
	if c, err := r.Cookie(f.sessionCookie(r)); err == nil {
		f.mu.Lock()
		delete(f.sessions, c.Value)
		f.mu.Unlock()
	}
	setCookie(w, r, f.sessionCookie(r), "")
	http.Redirect(w, r, f.route("/"), http.StatusFound)
}

// session returns the request's live gateway session. A session ends when it
// expires, arrives from another address, or its uid no longer resolves to the
// same DSM name (account deleted or renamed).
func (f *Front) session(w http.ResponseWriter, r *http.Request) (Subject, bool) {
	s, err := f.lookupSession(w, r)
	return s, err == nil
}

func (f *Front) lookupSession(w http.ResponseWriter, r *http.Request) (Subject, error) {
	c, err := r.Cookie(f.sessionCookie(r))
	if err != nil {
		return Subject{}, errSessionChanged
	}
	f.mu.Lock()
	s, ok := f.sessions[c.Value]
	f.mu.Unlock()
	if ok && !f.cfg.Now().After(s.expires) && s.ip == remoteIP(r) {
		err := f.checkAccount(r.Context(), c.Value, remoteIP(r), s)
		if err == nil {
			f.mu.Lock()
			current, exists := f.sessions[c.Value]
			f.mu.Unlock()
			if exists && current == s && f.cfg.Now().Before(s.expires) {
				return s.subject, nil
			}
		} else if !errors.Is(err, errSessionChanged) {
			return Subject{}, err
		}
	}
	f.mu.Lock()
	delete(f.sessions, c.Value)
	f.mu.Unlock()
	setCookie(w, r, f.sessionCookie(r), "")
	return Subject{}, errSessionChanged
}

// RevokeUID ends the gateway sessions of the account uid and, in oidc mode,
// the provider's pending codes and tokens for it.
func (f *Front) RevokeUID(uid int) {
	f.mu.Lock()
	for k, s := range f.sessions {
		if s.subject.UID == uid {
			delete(f.sessions, k)
		}
	}
	f.mu.Unlock()
	if f.cfg.Provider != nil {
		f.cfg.Provider.RevokeUID(uid)
	}
}

// setCookie sets a gateway cookie, or clears it when value is empty. It is
// Secure follows the browser's scheme, independently of the internal TLS hop.
func setCookie(w http.ResponseWriter, r *http.Request, name, value string) {
	c := &http.Cookie{Name: name, Value: value, Path: requestCookiePath(r), HttpOnly: true, Secure: requestSecure(r), SameSite: http.SameSiteLaxMode}
	if value == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

func remoteIP(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// rewrite builds the upstream request. Forwarding headers describe only this
// hop and identity headers come only from the gateway, so neither can be
// spoofed by the client. The gateway's cookies never reach the app.
func (f *Front) rewrite(pr *httputil.ProxyRequest) {
	pr.SetURL(f.upstream)
	pr.Out.Host = pr.In.Host
	out := pr.Out.Header
	stripped := map[string]bool{}
	for _, name := range append(f.cfg.Headers.all(), nativeClientIPHeader, nativeSchemeHeader, "X-Syno-User", "X-Syno-Admin", "Remote-User", "Remote-Groups", "Remote-Email", "Remote-Name", "X-SYNO-TOKEN", "X-Real-Ip", "X-Forwarded-Port", "X-Forwarded-Prefix", "X-Original-Url", "X-Rewrite-Url") {
		out.Del(name)
		stripped[http.CanonicalHeaderKey(name)] = true
	}
	// CGI-style servers read Remote_User as Remote-User, so an underscore
	// spelling of a stripped header goes too.
	for name := range out {
		if strings.Contains(name, "_") && stripped[http.CanonicalHeaderKey(strings.ReplaceAll(name, "_", "-"))] {
			delete(out, name)
		}
	}
	ip, proto := remoteIP(pr.In), requestScheme(pr.In)
	out.Set("X-Forwarded-For", ip)
	out.Set("X-Real-Ip", ip)
	out.Set("X-Forwarded-Proto", proto)
	out.Set("X-Forwarded-Host", pr.In.Host)
	port := (&url.URL{Host: pr.In.Host}).Port()
	if port == "" {
		port = "80"
		if proto == "https" {
			port = "443"
		}
	}
	out.Set("X-Forwarded-Port", port)
	f.stripGatewayCookies(pr.Out)
	if f.upstreamAuthenticated(pr.In) {
		return
	}
	if f.rewritesJSON(pr.In) || f.rewritesHTML(pr.In) || f.cfg.Browser != nil && isNavigation(pr.In) {
		out.Set("Accept-Encoding", "identity")
	}
	if f.rewritesJSON(pr.In) || f.rewritesHTML(pr.In) || f.cfg.Browser != nil && isNavigation(pr.In) {
		out.Del("If-None-Match")
		out.Del("If-Modified-Since")
		out.Del("Range")
		out.Del("If-Range")
	}
	s, ok := pr.In.Context().Value(subjectKey).(Subject)
	if !ok {
		return
	}
	switch f.cfg.Mode {
	case ModeHeader:
		h := f.cfg.Headers
		set := func(name, value string) {
			if name != "" {
				out.Set(name, value)
			}
		}
		set(h.User, s.Name)
		set(h.Name, s.Name)
		set(h.Email, emailAddress(s, f.cfg.EmailDomain))
		set(h.UID, strconv.Itoa(s.UID))
		set(h.Groups, f.cfg.Roles.value(s.Admin))
	case ModeToken:
		creds, err := f.cfg.Tokens.Credentials(pr.In.Context(), s)
		if err != nil {
			pr.Out.URL.Host = "" // fails the round trip: no request without the app's credentials
			return
		}
		for k, v := range creds {
			out[http.CanonicalHeaderKey(k)] = v
		}
	}
}

func (f *Front) stripGatewayCookies(r *http.Request) {
	var cookies []string
	for _, line := range r.Header.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			name, _, ok := strings.Cut(part, "=")
			if ok && !f.reservedCookie(strings.TrimSpace(name)) {
				cookies = append(cookies, part)
			}
		}
	}
	r.Header.Del("Cookie")
	if len(cookies) != 0 {
		r.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
}

func (f *Front) reservedCookie(name string) bool {
	return name == "id" || name == "did" || name == "_SSID" || name == "_CrPoSt" || name == "stay_login" || strings.HasPrefix(name, "synopkgland_sso_") || strings.HasPrefix(name, "spl_session_")
}

func (f *Front) rewritesJSON(r *http.Request) bool {
	for _, j := range f.cfg.RewriteJSON {
		if j.Method == r.Method && j.Path == r.URL.Path {
			return true
		}
	}
	return false
}

// modifyResponse points the provider's loopback authorization URL, and any
// absolute redirect to the upstream itself, at the browser's app origin.
func (f *Front) modifyResponse(res *http.Response) error {
	cookies := res.Header.Values("Set-Cookie")
	res.Header.Del("Set-Cookie")
	for _, value := range cookies {
		name, _, ok := strings.Cut(value, "=")
		if ok && !f.reservedCookie(strings.TrimSpace(name)) {
			if f.cfg.IngressPath != "" {
				cookieRes := http.Response{Header: http.Header{"Set-Cookie": {value}}}
				for _, c := range cookieRes.Cookies() {
					c.Domain = ""
					if !strings.HasPrefix(c.Path, f.cfg.IngressPath) {
						c.Path = f.cfg.IngressPath
					}
					res.Header.Add("Set-Cookie", c.String())
				}
			} else {
				res.Header.Add("Set-Cookie", value)
			}
		}
	}
	if f.upstreamAuthenticated(res.Request) {
		return nil
	}
	origin, _ := res.Request.Context().Value(originKey).(string)
	if loc := res.Header.Get("Location"); loc != "" {
		if public := f.publicURL(loc, origin); public != loc {
			res.Header.Set("Location", public)
			uncacheBrowserResponse(res)
		}
	}
	for _, j := range f.cfg.RewriteJSON {
		if j.Method == res.Request.Method && j.Path == res.Request.URL.Path {
			return f.rewriteJSONField(res, j.Field, origin)
		}
	}
	if err := f.rewriteBrowserHTML(res, origin); err != nil {
		return err
	}
	return f.injectBrowser(res)
}

func (f *Front) publicURL(loc, origin string) string {
	if f.cfg.Mode == ModeOIDC {
		authorize := f.cfg.Issuer + "/authorize"
		if rest, ok := strings.CutPrefix(loc, authorize); ok && (rest == "" || rest[0] == '?') {
			return origin + f.route("/_sso/authorize") + rest
		}
	}
	if u, err := url.Parse(loc); err == nil && u.Scheme == f.upstream.Scheme && u.Host == f.upstream.Host {
		public, _ := url.Parse(origin)
		u.Scheme, u.Host = public.Scheme, public.Host
		return u.String()
	}
	return loc
}

const maxRewriteBody = 1 << 20

func (f *Front) rewriteJSONField(res *http.Response, field, origin string) error {
	if !strings.Contains(res.Header.Get("Content-Type"), "json") || res.Header.Get("Content-Encoding") != "" {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxRewriteBody+1))
	if err != nil {
		return err
	}
	if len(raw) > maxRewriteBody {
		res.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(raw), res.Body), res.Body}
		return nil
	}
	res.Body.Close()
	var doc map[string]json.RawMessage
	var value string
	if json.Unmarshal(raw, &doc) == nil && json.Unmarshal(doc[field], &value) == nil {
		if public := f.publicURL(value, origin); public != value {
			uncacheBrowserResponse(res)
			doc[field], _ = json.Marshal(public)
			raw, _ = json.Marshal(doc)
		}
	}
	res.Body = io.NopCloser(bytes.NewReader(raw))
	res.ContentLength = int64(len(raw))
	res.Header.Set("Content-Length", strconv.Itoa(len(raw)))
	return nil
}
