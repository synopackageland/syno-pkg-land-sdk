package idgateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type schemeTransport func(*http.Request) (*http.Response, error)

func (f schemeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Emulate nginx's authenticated upstream, using a real TLS Unix socket. The
// client's URL (and cookie jar) retain the browser's external scheme.
func ingressSchemeClient(t *testing.T, cfg Config, h http.Handler) *http.Client {
	t.Helper()
	cfg.StateDir = t.TempDir()
	pair := nativeTestCertificate(t, cfg.StateDir)
	serverTLS, err := nativeTLSConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "gateway.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: nativeIngressHandler(cfg, h)}
	go server.Serve(tls.NewListener(listener, serverTLS))
	t.Cleanup(func() { server.Close() })
	ca := x509.NewCertPool()
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	ca.AddCert(cert)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: ca, ServerName: "gateway.invalid", Certificates: []tls.Certificate{pair}},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}
	t.Cleanup(transport.CloseIdleConnections)
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: schemeTransport(func(r *http.Request) (*http.Response, error) {
			inner := r.Clone(r.Context())
			inner.Host = r.URL.Host
			inner.Header.Set(nativeClientIPHeader, "192.0.2.7")
			inner.Header.Set("X-Synopkgland-Scheme", r.URL.Scheme)
			inner.URL.Scheme = "https"
			return transport.RoundTrip(inner)
		})}
}

func TestIngressTLSExternalSchemeSignIn(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			const prefix = "/synopkgland/App/"
			origin := scheme + "://gateway.invalid"
			up := newUpstream(t, nil)
			f, clock := newFront(t, ModeHeader, up, func(c *FrontConfig) { c.IngressPath = prefix })
			client := ingressSchemeClient(t, Config{Package: "App", NativeIngress: true, IngressPath: prefix}, f)
			request := func(method, path string) *http.Response {
				t.Helper()
				r, _ := http.NewRequest(method, origin+path, nil)
				r.Header.Set("Accept", "text/html")
				r.Header.Set("Origin", origin)
				// These client-controlled headers must never select the scheme.
				r.Header.Set("X-Forwarded-Proto", "attacker")
				res, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				return res
			}
			start := request("GET", prefix+"library?x=1")
			dest, _ := url.Parse(start.Header.Get("Location"))
			if start.StatusCode != 302 || dest.Scheme+"://"+dest.Host != origin || dest.Query().Get("return") != origin+prefix+"_sso/dsm-return" {
				t.Fatalf("wrong sign-in origin: %d %s", start.StatusCode, dest)
			}
			checkCookies := func(res *http.Response) {
				t.Helper()
				if len(res.Cookies()) == 0 {
					t.Fatal("missing sign-in cookies")
				}
				for _, c := range res.Cookies() {
					if c.Secure != (scheme == "https") || !c.HttpOnly || c.Path != prefix || c.Domain != "" {
						t.Fatalf("wrong cookie scope or scheme: %s", c)
					}
				}
			}
			checkCookies(start)
			assertion, err := SealAssertion(testKey, Assertion{Txn: dest.Query().Get("txn"), Origin: origin, Subject: alice, Expires: clock.now().Add(AssertionTTL).Unix()})
			if err != nil {
				t.Fatal(err)
			}
			res := request("GET", prefix+"_sso/dsm-return?"+url.Values{"txn": {dest.Query().Get("txn")}, "assertion": {assertion}}.Encode())
			if res.StatusCode != 302 || res.Header.Get("Location") != prefix+"library?x=1" {
				t.Fatalf("sign-in failed: %d", res.StatusCode)
			}
			checkCookies(res)
			if res.Header.Get("Strict-Transport-Security") != "" {
				t.Fatal("the gateway must not force HTTPS on the DSM HTTP entry")
			}
			res = request("POST", prefix+"api")
			if res.StatusCode != 200 {
				t.Fatalf("same-origin request refused: %d", res.StatusCode)
			}
			up.mu.Lock()
			defer up.mu.Unlock()
			port := "80"
			if scheme == "https" {
				port = "443"
			}
			if up.last.Header.Get("X-Forwarded-Proto") != scheme || up.last.Header.Get("X-Forwarded-Port") != port || up.last.Header.Get("Remote-User") != alice.Name {
				t.Fatalf("wrong upstream scheme or identity: %v", up.last.Header)
			}
		})
	}
}

func TestIngressSchemeTrustBoundary(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		cfg := Config{Package: "App", NativeIngress: true, IngressPath: "/reader/"}
		h := nativeIngressHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || requestScheme(r) != scheme || r.Header.Get(nativeSchemeHeader) != "" {
				t.Fatal("ingress lost transport authentication or trusted a raw header")
			}
			w.WriteHeader(204)
		}))
		for _, tc := range []struct {
			authenticated bool
			values        []string
			status        int
		}{{false, []string{scheme}, 403}, {true, nil, 400}, {true, []string{"ftp"}, 400}, {true, []string{"http, https"}, 400}, {true, []string{"http", "https"}, 400}, {true, []string{scheme}, 204}} {
			r := httptest.NewRequest("GET", "https://gateway.invalid/reader/", nil)
			if tc.authenticated {
				r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
			}
			r.Header.Set(nativeClientIPHeader, "192.0.2.7")
			r.Header[nativeSchemeHeader] = tc.values
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("authenticated=%v values=%v: %d", tc.authenticated, tc.values, w.Code)
			}
		}
	}
}

func TestIngressHTTPDoesNotReuseHTTPSCookieNames(t *testing.T) {
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), func(c *FrontConfig) { c.IngressPath = "/reader/" })
	client := ingressSchemeClient(t, Config{Package: "App", NativeIngress: true, IngressPath: "/reader/"}, f)
	var names []string
	for _, scheme := range []string{"https", "http"} {
		r, _ := http.NewRequest("GET", scheme+"://gateway.invalid/reader/", nil)
		r.Header.Set("Accept", "text/html")
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if len(res.Cookies()) != 1 {
			t.Fatal("missing binding cookie")
		}
		names = append(names, res.Cookies()[0].Name)
	}
	if names[0] == names[1] {
		t.Fatal("a stored Secure cookie blocks an HTTP cookie with the same name and path")
	}
}

func TestDirectSchemeIgnoresIngressAndForwardedHeaders(t *testing.T) {
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	for _, scheme := range []string{"http", "https"} {
		other := "https"
		if scheme == "https" {
			other = "http"
		}
		r := httptest.NewRequest("GET", scheme+"://gateway.invalid:18443/", nil)
		r.Header.Set("Accept", "text/html")
		r.Header.Set("X-Synopkgland-Scheme", other)
		r.Header.Set("X-Forwarded-Proto", other)
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), scheme+"://") || w.Result().Cookies()[0].Secure != (scheme == "https") {
			t.Fatalf("client headers changed direct scheme: %d %v", w.Code, w.Header())
		}
	}
}

func TestIngressSessionCookiesKeepHTTPSAndHTTPSeparate(t *testing.T) {
	const prefix = "/reader/"
	up := newUpstream(t, nil)
	f, clock := newFront(t, ModeHeader, up, func(c *FrontConfig) { c.IngressPath = prefix })
	client := ingressSchemeClient(t, Config{Package: "App", NativeIngress: true, IngressPath: prefix}, f)
	request := func(method, target string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest(method, target, nil)
		r.Header.Set("Accept", "text/html")
		u, _ := url.Parse(target)
		r.Header.Set("Origin", u.Scheme+"://"+u.Host)
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res
	}
	var sessionNames []string
	for i, scheme := range []string{"https", "http"} {
		origin := scheme + "://gateway.invalid"
		start := request("GET", origin+prefix)
		dest, _ := url.Parse(start.Header.Get("Location"))
		subject := alice
		if i == 1 {
			subject = bob
		}
		sealed, err := SealAssertion(testKey, Assertion{Txn: dest.Query().Get("txn"), Origin: origin, Subject: subject, Expires: clock.now().Add(AssertionTTL).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		res := request("GET", dest.Query().Get("return")+"?"+url.Values{"txn": {dest.Query().Get("txn")}, "assertion": {sealed}}.Encode())
		if res.StatusCode != 302 || len(res.Cookies()) != 1 {
			t.Fatal("session not established", res.StatusCode)
		}
		sessionNames = append(sessionNames, res.Cookies()[0].Name)
	}
	if sessionNames[0] == sessionNames[1] {
		t.Fatal("HTTP session collides with an existing Secure session cookie")
	}
	for _, scheme := range []string{"https", "http"} {
		res := request("GET", scheme+"://gateway.invalid"+prefix+"api")
		if res.StatusCode != 200 {
			t.Fatal("stored cookie lost", scheme, res.StatusCode)
		}
		want := alice.Name
		if scheme == "http" {
			want = bob.Name
		}
		up.mu.Lock()
		got := up.last.Header.Get("Remote-User")
		up.mu.Unlock()
		if got != want {
			t.Fatal("cross-scheme session reuse", scheme, got)
		}
	}
	if res := request("POST", "http://gateway.invalid"+prefix+"_sso/logout"); res.StatusCode != 302 {
		t.Fatal("HTTP logout failed")
	}
	if res := request("POST", "https://gateway.invalid"+prefix+"api"); res.StatusCode != 200 {
		t.Fatal("HTTP logout removed the HTTPS session")
	}
	if res := request("POST", "http://gateway.invalid"+prefix+"api"); res.StatusCode != 401 {
		t.Fatal("HTTP session survived logout")
	}
}
