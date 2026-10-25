package idgateway

import (
	"errors"
	"log"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/externalorigin"
)

// Connect's menu-only launch map sits beside its sign-in projection. Unlike the
// projection it lists private network addresses too: those are already public
// in Certificate Transparency logs, and only the DSM menu launcher reads them.
const DefaultLaunchMap = "/var/packages/SynoPkgConnect/var/spl/launch.json"
const FallbackLaunchMap = "/var/packages/SynoPkgConnect/target/spl/launch.json"

// Providers a launch map route may name. The name selects page text and the
// Connect scope only; it never widens where the launcher may send a browser.
const (
	ProviderCloudflare = "cloudflare"
	ProviderTailscale  = "tailscale"
	ProviderWorkers    = "workers"
	// ProviderManual is the App's own dsm_origin to manual_origin pair.
	ProviderManual = "manual"
	// ProviderCanonical is a single-origin App's only origin.
	ProviderCanonical = "canonical"
	// ProviderQuickConnect is Synology's relay, which cannot reach App ports.
	ProviderQuickConnect = "quickconnect"
	// ProviderDirect is the DSM hostname reached from outside the local
	// network, where a router may forward DSM's port but not the App's.
	ProviderDirect = "direct"
)

// LaunchMap (schema 1) maps each DSM address Connect serves to the addresses of
// the Apps approved on that same provider.
type LaunchMap struct {
	Schema int           `json:"schema"`
	Routes []LaunchRoute `json:"routes"`
}
type LaunchRoute struct {
	Provider string               `json:"provider"`
	DSM      string               `json:"dsm"`
	Apps     map[string]LaunchApp `json:"apps"`
}
type LaunchApp struct {
	Origin string `json:"origin"`
	Port   int    `json:"port"`
	State  string `json:"state"`
}

// launchOrigin canonicalizes a route address: private network addresses keep
// their port, public ones are https://<DNS hostname> as in the projection.
func launchOrigin(provider, raw string) (string, bool) {
	canonical := externalorigin.Canonical
	if provider == ProviderTailscale {
		canonical = externalorigin.CanonicalWithPort
	}
	out, err := canonical(raw)
	return out, err == nil
}

func (m LaunchMap) valid() bool {
	if m.Schema != 1 || len(m.Routes) > 8 {
		return false
	}
	for _, route := range m.Routes {
		if route.Provider != ProviderCloudflare && route.Provider != ProviderTailscale && route.Provider != ProviderWorkers || len(route.Apps) > 4096 {
			return false
		}
		if _, ok := launchOrigin(route.Provider, route.DSM); !ok {
			return false
		}
		for pkg, app := range route.Apps {
			if !namePattern.MatchString(pkg) || !validPort(app.Port, false) || app.State != "active" && app.State != "paused" {
				return false
			}
			if _, ok := launchOrigin(route.Provider, app.Origin); !ok {
				return false
			}
		}
	}
	return true
}

// MenuSources are the trusted inputs of one launcher request, read afresh: the
// launcher is a CGI that serves a single request per process.
type MenuSources struct {
	External   External
	State      ExternalState
	Projection ExternalProjection
	// Launch is nil when Connect wrote no launch map, as before Connect added it.
	Launch *LaunchMap
}

// readFirstExternal reads the first location that exists and can be traversed.
// A malformed first file never falls through to a stale second one.
func readFirstExternal(paths []string, v any, valid func() bool) (found bool) {
	for _, path := range paths {
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			continue
		}
		if _, err := readExternalFile(path, false, v); err != nil || !valid() {
			log.Printf("INFO external access input is invalid; treating it as absent")
			return false
		}
		return true
	}
	return false
}

// LoadMenuSources reads the App state_file, Connect's projection and Connect's
// launch map with the same file checks as the gateway. Invalid inputs are absent.
func LoadMenuSources(cfg Config) MenuSources {
	return loadMenuSources(cfg, []string{DefaultProjection, FallbackProjection}, []string{DefaultLaunchMap, FallbackLaunchMap})
}
func loadMenuSources(cfg Config, projections, launches []string) MenuSources {
	var src MenuSources
	if state, err := LoadExternalState(cfg); err == nil {
		src.State = state
	}
	if cfg.External == nil || cfg.External.Connect != "none" {
		var p ExternalProjection
		if readFirstExternal(projections, &p, func() bool { return p.valid() }) {
			src.Projection = p
		}
		var m LaunchMap
		if readFirstExternal(launches, &m, func() bool { return m.valid() }) {
			src.Launch = &m
		}
	}
	src.External = EffectiveExternal(cfg, src.State, src.Projection)
	return src
}

type MenuKind int

const (
	// MenuBaseline keeps the launcher's existing DSM hostname and App port.
	MenuBaseline MenuKind = iota
	// MenuRedirect opens Origin, a trusted App address.
	MenuRedirect
	// MenuFallback explains why the App cannot open from this DSM address.
	MenuFallback
)

const (
	MenuNotEnabled   = "not_enabled"
	MenuPaused       = "paused"
	MenuQuickConnect = "quickconnect"
	MenuSingleLocal  = "single_local"
)

type MenuDecision struct {
	Kind     MenuKind
	Origin   string
	Provider string
	Reason   string
}

type menuRoute struct {
	provider string
	dsm      string
	app      *LaunchApp
}

// menuRoutes lists the trusted DSM-to-App pairs for this App: the App's own
// manual pair first, then Connect's launch map, or the projection's public
// pair for a Connect that writes no launch map.
func menuRoutes(cfg Config, src MenuSources) []menuRoute {
	var routes []menuRoute
	if src.State.valid() && src.External.Manual != "" {
		if dsm, ok := optionalExternalOrigin(src.State.DSMOrigin); ok && dsm != "" {
			routes = append(routes, menuRoute{ProviderManual, dsm, &LaunchApp{Origin: src.External.Manual, Port: nativePublicPort(cfg), State: "active"}})
		}
	}
	if cfg.External != nil && cfg.External.Connect == "none" {
		return routes
	}
	if src.Launch != nil && src.Launch.valid() {
		for _, route := range src.Launch.Routes {
			dsm, _ := launchOrigin(route.Provider, route.DSM)
			r := menuRoute{provider: route.Provider, dsm: dsm}
			if app, ok := route.Apps[cfg.Package]; ok {
				app.Origin, _ = launchOrigin(route.Provider, app.Origin)
				r.app = &app
			}
			routes = append(routes, r)
		}
		return routes
	}
	if src.Projection.valid() && src.Projection.DSM.Origin != "" {
		dsm, _ := optionalExternalOrigin(src.Projection.DSM.Origin)
		r := menuRoute{provider: ProviderCloudflare, dsm: dsm}
		if app, ok := src.Projection.Apps[cfg.Package]; ok && app.Public.Origin != "" {
			origin, _ := optionalExternalOrigin(app.Public.Origin)
			r.app = &LaunchApp{Origin: origin, Port: app.Port, State: "active"}
		}
		routes = append(routes, r)
	}
	return routes
}

// menuAuthority is the request's canonical https authority, or "" for a host
// no route can name (an IP, a malformed host).
func menuAuthority(host string, https bool) string {
	if !https {
		return ""
	}
	out, err := externalorigin.CanonicalWithPort("https://" + host)
	if err != nil {
		return ""
	}
	return out
}

// quickConnectRelay reports Synology's relay hostnames. Direct QuickConnect
// hostnames resolve to the NAS itself, where the DSM hostname and App port can
// work, so they keep the existing destination.
func quickConnectRelay(host string) bool {
	host = strings.ToLower(hostname(host))
	for _, zone := range []string{".quickconnect.to", ".quickconnect.cn"} {
		if strings.HasSuffix(host, zone) {
			return !strings.HasSuffix(host, ".direct"+zone)
		}
	}
	return false
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// Name suffixes of private networks, and reserved names that never resolve on
// the Internet. A tailnet reaches every port of the NAS.
var lanSuffixes = []string{".local", ".lan", ".home", ".home.arpa", ".internal", ".intranet", ".localdomain", ".corp", ".private", ".ts.net", ".example", ".test", ".invalid", ".localhost"}

// Special-purpose IPv4 ranges that are not globally reachable, beyond the
// private, loopback and link-local ones netip reports.
var nonGlobalIPv4 = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

func globalIPv4(a netip.Addr) bool {
	if !a.Is4() || !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range nonGlobalIPv4 {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// outsideOrigin is the DSM hostname with the App port when the browser reached
// DSM from outside the local network: a DNS name that is not a single label or
// a private suffix, a global IPv4 address, or a direct QuickConnect hostname
// that encodes one. Everything else, including IPv6 literals and malformed
// names a CSP source cannot express, keeps the existing plain redirect.
func outsideOrigin(host string, port int) (string, bool) {
	host = strings.ToLower(hostname(host))
	if port <= 0 || port > 65535 || host == "" || len(host) > 253 {
		return "", false
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if !globalIPv4(a) {
			return "", false
		}
	} else {
		labels := strings.Split(host, ".")
		if len(labels) < 2 || strings.Trim(labels[len(labels)-1], "0123456789") == "" {
			return "", false
		}
		for _, label := range labels {
			if !dnsLabel.MatchString(label) {
				return "", false
			}
		}
		if host == "localhost" {
			return "", false
		}
		for _, suffix := range lanSuffixes {
			if strings.HasSuffix(host, suffix) {
				return "", false
			}
		}
		for _, zone := range []string{".direct.quickconnect.to", ".direct.quickconnect.cn"} {
			if strings.HasSuffix(host, zone) {
				a, err := netip.ParseAddr(strings.ReplaceAll(labels[0], "-", "."))
				if err != nil || !globalIPv4(a) {
					return "", false
				}
			}
		}
	}
	return "https://" + host + ":" + strconv.Itoa(port), true
}

// MenuDestination decides where the DSM menu opens this App. The request host
// only selects among trusted pairs; the only address it supplies is its own
// hostname with the App port, as before. LAN hosts keep that destination with
// a plain redirect; hosts outside the local network first check it.
func MenuDestination(cfg Config, src MenuSources, host string, https bool) MenuDecision {
	// A single-origin App opens its canonical origin from anywhere.
	if canonical := src.External.Canonical(cfg.External); canonical != "" {
		return MenuDecision{Kind: MenuRedirect, Origin: canonical, Provider: ProviderCanonical}
	}
	if !cfg.NativeIngress {
		return MenuDecision{Kind: MenuBaseline}
	}
	single := cfg.External != nil && cfg.External.SingleOrigin
	if authority := menuAuthority(host, https); authority != "" {
		var matched, paused *menuRoute
		routes := menuRoutes(cfg, src)
		for i := range routes {
			route := &routes[i]
			if route.dsm != authority {
				continue
			}
			if matched == nil {
				matched = route
			}
			if single || route.app == nil || route.app.Origin == "" {
				continue
			}
			if route.app.State == "active" && route.app.Port == nativePublicPort(cfg) {
				return MenuDecision{Kind: MenuRedirect, Origin: route.app.Origin, Provider: route.provider}
			}
			// Paused, or approved for a port the App no longer listens on.
			if paused == nil {
				paused = route
			}
		}
		switch {
		case matched != nil && single:
			// Without a canonical origin, the App signs in only on its local
			// addresses: a provider address would fail sign-in.
			return MenuDecision{Kind: MenuFallback, Provider: matched.provider, Reason: MenuSingleLocal}
		case paused != nil:
			return MenuDecision{Kind: MenuFallback, Provider: paused.provider, Reason: MenuPaused}
		case matched != nil:
			return MenuDecision{Kind: MenuFallback, Provider: matched.provider, Reason: MenuNotEnabled}
		}
	}
	if quickConnectRelay(host) {
		return MenuDecision{Kind: MenuFallback, Provider: ProviderQuickConnect, Reason: MenuQuickConnect}
	}
	if origin, ok := outsideOrigin(host, nativePublicPort(cfg)); ok {
		return MenuDecision{Kind: MenuRedirect, Origin: origin, Provider: ProviderDirect}
	}
	return MenuDecision{Kind: MenuBaseline}
}
