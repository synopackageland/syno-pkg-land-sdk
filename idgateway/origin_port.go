package idgateway

import (
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/synopackageland/syno-pkg-land-sdk/installsettings"
)

// OriginVersion identifies the second-origin contract: Config.Origin serves a
// separate, administrator-only HTTPS port through DSM's nginx to a package
// Unix socket, and the dsm-passthrough cookie mode.
const OriginVersion = 1

// OriginCookiesDSMPassthrough forwards DSM's own browser cookies (_SSID,
// _CrPoSt, stay_login) in both directions. Only the gateway's cookies and the
// DSM sign-in cookies id and did, which belong to the DSM origin on the same
// host, are removed.
const OriginCookiesDSMPassthrough = "dsm-passthrough"

// OriginConfig is a second browser origin of the package: an HTTPS port
// owned by DSM's nginx (DSM's certificate), forwarded over a separate mutually
// authenticated socket to the gateway, then to Upstream. It always requires a
// DSM administrator. Headers, when User is set, carry the signed-in
// administrator to the upstream; otherwise no identity is sent.
type OriginConfig struct {
	HTTPS    int         `json:"https"`
	Upstream string      `json:"upstream"`
	Cookies  string      `json:"cookies,omitempty"`
	Headers  HeaderNames `json:"headers,omitempty"`
}

// originPortKey is the installer-selected port key (install.ports).
const originPortKey = "gateway_origin"

func noExternal() External { return External{} }

func originSocketPath(pkg string) string { return "/tmp/synopkgland-gateway-" + pkg + "-origin.sock" }

func originCookieName(pkg string) string { return "synopkgland_sso_" + pkg + "_origin" }

func (c Config) validateOrigin() error {
	o := c.Origin
	if o == nil {
		return nil
	}
	if !c.NativeIngress {
		return errors.New("origin requires native_ingress")
	}
	if c.ComposeIngress != nil {
		return errors.New("origin cannot be combined with compose_ingress")
	}
	if !installsettings.ValidPort(o.HTTPS) {
		return errors.New("origin.https must be an unreserved port between 1024 and 65535")
	}
	if o.HTTPS == c.Listen.HTTP || o.HTTPS == c.Listen.HTTPS || c.OIDC != nil && o.HTTPS == c.OIDC.IssuerPort {
		return errors.New("origin.https must differ from the gateway's other ports")
	}
	path, err := parseUnixUpstream(c.Package, o.Upstream, false)
	if err != nil {
		return fmt.Errorf("origin: %w", err)
	}
	if path == "" {
		return fmt.Errorf("origin.upstream must be unix:/var/packages/%s/var/run/<name>.sock", c.Package)
	}
	switch o.Cookies {
	case "", OriginCookiesDSMPassthrough:
	default:
		return errors.New("origin.cookies must be dsm-passthrough or empty")
	}
	if o.Headers != (HeaderNames{}) && o.Headers.User == "" {
		return errors.New("origin.headers needs user")
	}
	return nil
}

// originFront is the second origin's Front. It shares the gateway key, so the
// authorize CGI's assertions work, but has its own sessions and cookie name;
// assertions are bound to the origin they were issued for.
func (c Config) originFront(key []byte, ports DSMPorts) (*Front, error) {
	o := c.Origin
	mode := ModeNone
	if o.Headers.User != "" {
		mode = ModeHeader
	}
	return NewFront(FrontConfig{
		PublicListen: Listen{HTTPS: o.HTTPS}, Ready: c.Ready, Mode: mode, Headers: o.Headers, Roles: c.Roles,
		Entry: "admin-only", Membership: NewDirectory(), Accounts: NewDirectory(), SessionTTL: c.SessionTTL(),
		// DSM long-polls: no response header timeout on this origin.
		Upstream: o.Upstream, Key: key, AuthorizeCGI: c.AuthorizeCGI,
		DSMPorts: ports, Package: c.Package, EmailDomain: c.EmailDomain, StateDir: c.StateDir,
		CookieName: originCookieName(c.Package), Cookies: o.Cookies, origin: true,
		// Never a public or Connect origin: only the declared port is accepted.
		External: noExternal, ExternalFresh: noExternal,
	})
}

// originIngressHandler admits only DSM nginx's authenticated hop for the
// origin port; the browser authority must name that port.
func originIngressHandler(cfg Config, next http.Handler) http.Handler {
	scoped := cfg
	scoped.IngressPath, scoped.External, scoped.Listen = "", nil, Listen{HTTPS: cfg.Origin.HTTPS}
	return nativeIngressHandler(scoped, next)
}

func originListener(cfg Config) (net.Listener, error) {
	return nativeListenerAt(cfg, originSocketPath(cfg.Package))
}
