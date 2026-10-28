package idgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/containerpreflight"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/sharepreflight"
	"path/filepath"
)

// GroupMembership must resolve fresh NSS membership; client headers and a
// package-writable cached group list are not authority.
type GroupMembership interface {
	Groups(context.Context, string) ([]string, error)
}

// ErrGroupRequired means an authenticated account is outside the admitted groups.
var ErrGroupRequired = errors.New("app entry requires an admitted DSM group")

func admissionError(ctx context.Context, s Subject, access string, wanted []string, m GroupMembership) error {
	if access == "" || access == "dsm-users" || access == "public" {
		return nil
	}
	if access == "dsm-admins" {
		admin, err := administrator(ctx, s.Name, m)
		if err != nil {
			return err
		}
		if admin {
			return nil
		}
		return api.ErrAdminRequired
	}
	if m == nil {
		return errors.New("gateway membership resolver unavailable")
	}
	groups, err := m.Groups(ctx, s.Name)
	if err != nil {
		return err
	}
	for _, g := range wanted {
		if slices.Contains(groups, g) {
			return nil
		}
	}
	return ErrGroupRequired
}

// AdministratorMembership is an optional native role provider for a directory
// adapter. Group strings alone never establish DSM administrator membership.
type AdministratorMembership interface {
	IsAdministrator(context.Context, string) (bool, error)
}

func administrator(ctx context.Context, name string, m GroupMembership) (bool, error) {
	if role, ok := m.(AdministratorMembership); ok {
		return role.IsAdministrator(ctx, name)
	}
	return dsm.IsAdministrator(ctx, name)
}

func permitted(ctx context.Context, s Subject, access string, wanted []string, m GroupMembership) bool {
	return admissionError(ctx, s, access, wanted, m) == nil
}
func (f *Front) admit(r *http.Request, s Subject) error {
	return f.admitClient(r.Context(), s, remoteIP(r))
}

func (f *Front) admitClient(ctx context.Context, s Subject, ip string) error {
	if err := admissionError(ctx, s, f.cfg.Access, f.cfg.Groups, f.cfg.Membership); err != nil {
		return err
	}
	if f.cfg.Entry != "app-privilege" {
		return nil
	}
	admin, err := administrator(ctx, s.Name, f.cfg.Membership)
	if err != nil {
		return err
	}
	if admin {
		return nil
	}
	allowed, err := f.cfg.appPrivilege(ctx, s.Name, "SYNO.SDS."+f.cfg.Package+".Instance", ip)
	if err != nil {
		return err
	}
	if !allowed {
		return api.ErrAppPrivilegeDenied
	}
	return nil
}

// AccessVerifier applies the same configured admission before any assertion is
// signed, including OIDC authorizations, and before group/admin gateway access.
type AccessVerifier struct {
	Next       SessionVerifier
	Access     string
	Groups     []string
	Membership GroupMembership
}

func (v AccessVerifier) Authenticate(r *http.Request) (api.Identity, error) {
	id, err := v.Next.Authenticate(r)
	if err != nil {
		return id, err
	}
	if err := admissionError(r.Context(), Subject{Name: id.User, Admin: id.Admin}, v.Access, v.Groups, v.Membership); err != nil {
		return api.Identity{}, err
	}
	return id, nil
}

// EntryHandler redirects a classless DSM url launcher to the configured native
// gateway port, retaining the NAS hostname instead of hardcoding loopback in a
// browser URL. It never appends DSM credentials to the URL.
func EntryHandler(cfg Config) http.Handler {
	gate, _ := dsm.NewEntryGate(dsm.NewAuthenticator(), cfg.IssuerEntry(), cfg.Package, cfg.StateDir)
	var auth api.Authenticator
	if gate != nil {
		auth = AccessVerifier{Next: gate, Access: cfg.Access, Groups: cfg.Groups, Membership: NewDirectory()}
	}
	return entryHandlerWithShares(cfg, containerpreflight.Check, func() error {
		return sharepreflight.Check(filepath.Join("/var/packages", cfg.Package, "target/compose"), filepath.Join("/var/packages", cfg.Package, "var"))
	}, auth)
}
func entryHandler(cfg Config, preflight func() error) http.Handler {
	return entryHandlerWithShares(cfg, preflight, nil, nil)
}
func entryHandlerWithShares(cfg Config, preflight func() error, shares func() error, auth api.Authenticator) http.Handler {
	return entryHandlerWithMenu(cfg, preflight, shares, auth, func() MenuSources { return LoadMenuSources(cfg) }, connectInstalled)
}
func entryHandlerWithExternal(cfg Config, preflight func() error, shares func() error, auth api.Authenticator, external func() External) http.Handler {
	return entryHandlerWithMenu(cfg, preflight, shares, auth, func() MenuSources { return MenuSources{External: external()} }, func() bool { return false })
}
func entryHandlerWithMenu(cfg Config, preflight func() error, shares func() error, auth api.Authenticator, sources func() MenuSources, connect func() bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var loaded *MenuSources
		menu := func() MenuSources {
			if loaded == nil {
				src := sources()
				loaded = &src
			}
			return *loaded
		}
		external := func() External { return menu().External }
		post := r.Method == http.MethodPost
		if r.Method != http.MethodGet && !post || !validRequestAuthority(r) || validateLaunchPath(cfg.LaunchPath) != nil {
			http.Error(w, "invalid entry", 400)
			return
		}
		if post && (!cfg.NativeIngress || !prepareMenuPost(w, r)) {
			if !cfg.NativeIngress {
				http.Error(w, "invalid entry", 400)
			}
			return
		}
		if cfg.ContainerApp {
			if err := preflight(); err != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
				w.WriteHeader(http.StatusServiceUnavailable)
				if p, ok := err.(*containerpreflight.Failure); ok {
					fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Container Manager required</title><p data-i18n="%s" data-error-code="%s">%s</p>`, html.EscapeString(p.MessageKey), html.EscapeString(p.Code), html.EscapeString(p.Message))
				} else {
					fmt.Fprint(w, "Container Manager/Docker is unavailable. Install Container Manager from Package Center, then start this App again.")
				}
				return
			}
		}
		if cfg.ContainerApp && shares != nil {
			if err := shares(); err != nil || r.URL.Query().Get("sharedfolder") == "retry" {
				if auth == nil {
					writeShareSignIn(w, r)
					return
				}
				if _, e := auth.Authenticate(r); e != nil {
					writeShareSignIn(w, r)
					return
				}
				writeSharePermission(w, r, err)
				return
			}
		}
		host := r.URL.Hostname()
		if host == "" {
			host = hostname(r.Host)
		}
		scheme, port := "http", cfg.Listen.HTTP
		if cfg.Listen.HTTPS != 0 {
			scheme, port = "https", cfg.Listen.HTTPS
		}
		if cfg.NativeIngress {
			scheme, port = "https", nativePublicPort(cfg)
		}
		if host == "" || port == 0 {
			http.Error(w, "entry unavailable", 503)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		target := scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"
		// DSM's classless URL launcher forwards launchParam as query values.
		// Reuse #37's bounded transaction/port resume without injecting a script
		// into a server-managed OIDC consumer's HTML.
		query := r.URL.Query()
		values, resume := query["ssoResume"]
		if nested, present := query["launchParam"]; present && cfg.OIDC.resumesColdLogin() {
			if len(nested) != 1 || len(nested[0]) > 4096 || resume {
				http.Error(w, "invalid sign-in resume", 400)
				return
			}
			if nested[0] != "" {
				inner, err := resumeLaunchParam(nested[0])
				if err != nil || len(inner) != 1 {
					http.Error(w, "invalid sign-in resume", 400)
					return
				}
				values, resume = inner["ssoResume"]
				if !resume {
					http.Error(w, "invalid sign-in resume", 400)
					return
				}
			}
		}
		if resume {
			if cfg.Mode != ModeOIDC || !cfg.OIDC.resumesColdLogin() || len(values) != 1 {
				http.Error(w, "invalid sign-in resume", 400)
				return
			}
			token, valid := parseResume(values[0])
			if !valid || !token.Public && token.Port != port {
				if strings.Contains(values[0], ":x") {
					http.Redirect(w, r, target, http.StatusFound)
				} else {
					http.Error(w, "invalid sign-in resume", 400)
				}
				return
			}
			q := url.Values{"txn": {token.Txn}, "return": {target + strings.TrimPrefix(ReturnPath, "/")}, "login_attempt": {"1"}}
			if token.Public {
				origin := token.origin(external())
				if origin == "" {
					http.Redirect(w, r, target, http.StatusFound)
					return
				}
				q.Set("return", origin+ReturnPath)
				q.Set("resume", values[0])
			}
			http.Redirect(w, r, requestOrigin(r)+cfg.AuthorizeCGI+"?"+q.Encode(), http.StatusFound)
			return
		}
		// The menu opens the App where the browser can reach it: a single-origin
		// App's canonical origin, or the App's address on the provider the user
		// reached DSM through. The host only selects among trusted addresses;
		// LAN and unknown hosts keep the destination above. Resume above keeps
		// the host that started the transaction.
		decision := MenuDestination(cfg, menu(), r.Host, r.TLS != nil)
		if post && decision.Kind == MenuBaseline {
			http.Error(w, "invalid entry", 400)
			return
		}
		if decision.Kind == MenuRedirect {
			target = decision.Origin + "/"
		}
		// A menu target comes only from package configuration. Resume always uses
		// the issuer root above, independently of any App query or fragment.
		if cfg.LaunchPath != "" {
			target = strings.TrimSuffix(target, "/") + cfg.LaunchPath
		}
		if decision.Kind != MenuBaseline && cfg.NativeIngress {
			writeMenuPage(w, r, cfg, decision, target, auth, connect())
			return
		}
		if !cfg.NativeIngress {
			locale, messages := denialMessages(r)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Language", locale)
			w.Header().Add("Vary", "Accept-Language")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset="utf-8"><title>%s</title><p data-i18n="gateway.direct_notice">%s</p><a data-i18n="gateway.direct_continue" href="%s">%s</a></html>`, locale, html.EscapeString(messages["direct_title"]), html.EscapeString(messages["direct_notice"]), html.EscapeString(target), html.EscapeString(messages["direct_continue"]))
			return
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
}

// resumesColdLogin reports whether the url launcher may resume a cold DSM
// sign-in: native OIDC login and the browser bridge both start from it.
func (o *OIDCConfig) resumesColdLogin() bool {
	return o != nil && (o.NativeLogin || o.Browser != nil)
}

// The SDK emits typed JSON resume values. Normalize DSM's bounded URL-launch
// representation and the former query form, never extra or duplicate keys.
func resumeLaunchParam(raw string) (url.Values, error) {
	return resumeLaunchValue(raw, true)
}
func resumeLaunchValue(raw string, indexed bool) (url.Values, error) {
	if !strings.HasPrefix(raw, "{") {
		values, err := url.ParseQuery(raw)
		if err != nil || values["ssoResume"] != nil || !indexed {
			return values, err
		}
		// DSM's classless URL handler serializes a string's character indexes.
		// Reconstruct one bounded contiguous ASCII string, then apply the exact
		// same resume-only grammar. Never recursively decode arbitrary input.
		if len(values) == 0 || len(values) > 256 {
			return nil, errors.New("invalid indexed resume")
		}
		var text strings.Builder
		for n := 0; n < len(values); n++ {
			part := values[strconv.Itoa(n)]
			if len(part) != 1 || len(part[0]) != 1 || part[0][0] < 32 || part[0][0] > 126 {
				return nil, errors.New("invalid indexed resume")
			}
			text.WriteString(part[0])
		}
		return resumeLaunchValue(text.String(), false)
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	open, err := dec.Token()
	if err != nil || open != json.Delim('{') {
		return nil, errors.New("invalid resume object")
	}
	key, err := dec.Token()
	if err != nil || key != "ssoResume" {
		return nil, errors.New("invalid resume key")
	}
	var value string
	if err = dec.Decode(&value); err != nil {
		return nil, errors.New("invalid resume value")
	}
	close, err := dec.Token()
	if err != nil || close != json.Delim('}') {
		return nil, errors.New("extra resume fields")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, errors.New("extra resume data")
	}
	return url.Values{"ssoResume": {value}}, nil
}
func (f *Front) composeSettings(w http.ResponseWriter, r *http.Request, s Subject) {
	if err := admissionError(r.Context(), s, "dsm-admins", nil, f.cfg.Membership); err != nil {
		f.writeFailure(w, r, admissionCode(err), r.URL.RequestURI(), true)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "COMPOSE_PROJECT_CONTROL_UNAVAILABLE", "state": "unsupported", "purpose": "compose-mount"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	fmt.Fprint(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Shared Folder access</title><style>body{font:16px system-ui;margin:40px auto;padding:0 20px;max-width:720px;line-height:1.6}code{overflow-wrap:anywhere}</style><h1>Shared Folder access</h1><p>This app uses a fixed package account. It does not inherit each signed-in user's File Station permissions. Files visible to users depend on the app's own library and sharing settings.</p><p>Read-only access prevents source changes. Read/write access may allow the app to modify or delete your files.</p><p>The app has not started. Automatic mapping is unavailable until an official project control and ACL adapter has been verified. No filesystem grant has been created.</p><p>Status: <code>COMPOSE_PROJECT_CONTROL_UNAVAILABLE</code></p></html>`)
}
func (f *Front) boundedBody(w http.ResponseWriter, r *http.Request) *http.Request {
	max := f.cfg.MaxUploadBytes
	if max <= 0 {
		max = 16 << 30
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	return r
}

func (f *Front) serveProxy(w http.ResponseWriter, r *http.Request) {
	if f.cfg.Ready != nil && !f.cfg.Ready(r.Context()) {
		writeStarting(w, r)
		return
	}
	max := f.cfg.MaxUploadBytes
	if max <= 0 {
		max = 16 << 30
	}
	if r.ContentLength > max {
		http.Error(w, "upload exceeds max_upload_bytes", http.StatusRequestEntityTooLarge)
		return
	}
	if isUpgrade(r) {
		if _, protected := r.Context().Value(subjectKey).(Subject); protected {
			if c, err := r.Cookie(f.cookie); err == nil {
				connection := &checkedUpgrade{done: make(chan struct{})}
				r = r.WithContext(context.WithValue(r.Context(), upgradeKey, upgradeSession{id: c.Value, ip: remoteIP(r), connection: connection}))
				w = &upgradeWriter{ResponseWriter: w, connection: connection}
			}
		}
	}
	f.proxy.ServeHTTP(w, f.boundedBody(w, r))
}
