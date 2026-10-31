// Package outbound restricts untrusted HTTP destinations to public networks.
// It is suitable for URL downloads and registry requests, not DSM API calls.
package outbound

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var errDestination = errors.New("outbound destination is not a public HTTP endpoint")

// ValidateURL checks syntax and literal destinations. Hostnames are checked
// again after resolution by NewTransport, on every new connection.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" || len(raw) > 4096 {
		return errDestination
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.ContainsAny(host, "%\\\x00") {
		return errDestination
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return errDestination
		}
	}
	if ip, e := netip.ParseAddr(host); e == nil && !publicIP(ip) {
		return errDestination
	}
	return nil
}

// These special-purpose ranges must not be used as a route to local services.
// In particular, IsGlobalUnicast alone includes private and metadata addresses.
var special = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("168.63.129.16/32"),
	// Translation and tunnelling can encode a forbidden IPv4 destination.
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Only the global IPv6 unicast allocation is routable by this policy.
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range special {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

type lookupFunc func(context.Context, string, string) ([]netip.Addr, error)
type dialFunc func(context.Context, string, string) (net.Conn, error)

func dialPublic(ctx context.Context, network, address string, lookup lookupFunc, dial dialFunc) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errDestination
	}
	var ips []netip.Addr
	if ip, e := netip.ParseAddr(host); e == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, err = lookup(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	if len(ips) == 0 {
		return nil, errDestination
	}
	// Reject mixed public/private answers too; never fall back to a local IP.
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, errDestination
		}
	}
	for _, ip := range ips {
		var conn net.Conn
		conn, err = dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, err
}

type guardedTransport struct{ http.RoundTripper }

func (t guardedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := ValidateURL(r.URL.String()); err != nil {
		return nil, err
	}
	return t.RoundTripper.RoundTrip(r)
}

// NewTransport returns an independent transport with no environment proxy and
// no alternate TLS dialer. It validates every request (including redirects),
// resolves and checks every address, and dials the checked IP without a second
// DNS lookup. TLS still authenticates the original hostname. Replacing this
// transport in an app opts out of the policy. responseHeaderTimeout bounds
// the wait for headers; non-positive values use 30 seconds.
func NewTransport(responseHeaderTimeout time.Duration) http.RoundTripper {
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = 30 * time.Second
	}
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialPublic(ctx, network, address, net.DefaultResolver.LookupNetIP, d.DialContext)
		},
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: responseHeaderTimeout,
		MaxResponseHeaderBytes: 32 << 10, DisableCompression: true, DisableKeepAlives: true,
	}
	return guardedTransport{tr}
}
