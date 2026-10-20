package idgateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nativeTestCertificate(t *testing.T, state string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gateway.invalid"},
		DNSNames: []string{"gateway.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, _ := x509.MarshalPKCS8PrivateKey(key)
	public := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "native-cert.pem"), public, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "native-key.pem"), private, 0600); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(public, private)
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func TestNativeIngressRequiresMutualTLSAndPreservesAuthority(t *testing.T) {
	cfg := Config{Package: "App", NativeIngress: true, Listen: Listen{HTTPS: 18443}, StateDir: t.TempDir()}
	pair := nativeTestCertificate(t, cfg.StateDir)
	serverTLS, err := nativeTLSConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var seen atomic.Bool
	h := nativeIngressHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(true)
		if requestOrigin(r) != "https://nas.example:18443" || r.RemoteAddr != "192.0.2.7:0" || r.Header.Get(nativeClientIPHeader) != "" {
			t.Errorf("untrusted authority or peer: %s %s", requestOrigin(r), r.RemoteAddr)
		}
		w.WriteHeader(204)
	}))
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = serverTLS
	srv.StartTLS()
	defer srv.Close()
	root := x509.NewCertPool()
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	root.AddCert(leaf)
	clientTLS := &tls.Config{RootCAs: root, ServerName: "gateway.invalid", MinVersion: tls.VersionTLS12}
	request := func(c *tls.Config) (*http.Response, error) {
		transport := &http.Transport{TLSClientConfig: c}
		defer transport.CloseIdleConnections()
		r, _ := http.NewRequest("GET", srv.URL, nil)
		r.Host = "nas.example:18443"
		r.Header.Set(nativeClientIPHeader, "192.0.2.7")
		return (&http.Client{Transport: transport}).Do(r)
	}
	if res, err := request(clientTLS); err == nil {
		res.Body.Close()
		t.Fatal("anonymous client entered private ingress")
	}
	if seen.Load() {
		t.Fatal("anonymous request reached handler")
	}
	clientTLS.Certificates = []tls.Certificate{pair}
	res, err := request(clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 || !seen.Load() {
		t.Fatal("authenticated proxy rejected", res.StatusCode)
	}
	os.Chmod(filepath.Join(cfg.StateDir, "native-key.pem"), 0644)
	if _, err := nativeTLSConfig(cfg); err == nil {
		t.Fatal("world-readable ingress key accepted")
	}
}

func TestNativeIngressSessionCSRFAndCookieScope(t *testing.T) {
	f, clock := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	cfg := Config{NativeIngress: true, Listen: Listen{HTTPS: 18443}}
	private := nativeIngressHandler(cfg, f)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
		r.Header.Set(nativeClientIPHeader, "192.0.2.7")
		private.ServeHTTP(w, r)
	})
	b := newBrowser(t, h)
	start := b.do("GET", "https://nas.example:18443/", navigate...)
	w := b.do("GET", dsmBounce(t, start, alice, clock.t), navigate...)
	if w.Code != 302 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if !c.Secure || !c.HttpOnly || c.Domain != "" || c.Path != "/" {
			t.Fatal("cookie escaped native HTTPS origin", c)
		}
	}
	if w := b.do("POST", "https://nas.example:18443/update", "Origin", "https://nas.example:5001"); w.Code != 403 {
		t.Fatal("DSM origin accepted as App origin", w.Code)
	}
	if w := b.do("POST", "https://nas.example:18443/update", "Origin", "https://nas.example:18443"); w.Code != 200 {
		t.Fatal("native origin refused", w.Code, w.Body.String())
	}
	for _, ip := range []string{"", "garbage", "192.0.2.7,192.0.2.8"} {
		r := httptest.NewRequest("GET", "https://nas.example:18443/", nil)
		r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
		r.Header.Set(nativeClientIPHeader, ip)
		out := httptest.NewRecorder()
		private.ServeHTTP(out, r)
		if out.Code < 400 {
			t.Fatal("invalid client binding accepted", ip)
		}
	}
}

func TestNativeRunDoesNotExposeAnUnauthenticatedProxyPort(t *testing.T) {
	state := t.TempDir()
	cfg := Config{Package: "native-test-" + strconv.FormatInt(time.Now().UnixNano(), 36), Mode: ModeNone,
		Access: "public", NativeIngress: true, Listen: Listen{HTTPS: freePort(t)}, StateDir: state,
		AuthorizeCGI: "/webman/3rdparty/App/sso/authorize.cgi", Upstream: newUpstream(t, nil).URL}
	pair := nativeTestCertificate(t, state)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, DSMPorts{HTTP: 5000, HTTPS: 5001}, nil) }()
	var conn net.Conn
	var err error
	for i := 0; i < 100; i++ {
		conn, err = net.DialTimeout("unix", nativeSocketPath(cfg.Package), 20*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Listen.HTTPS)), 100*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("gateway stole DSM nginx's public port")
	}
	root := x509.NewCertPool()
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	root.AddCert(leaf)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: root, ServerName: "gateway.invalid", Certificates: []tls.Certificate{pair}},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", nativeSocketPath(cfg.Package))
		}}
	defer transport.CloseIdleConnections()
	r, _ := http.NewRequest("GET", "https://nas.example:"+strconv.Itoa(cfg.Listen.HTTPS)+"/", nil)
	r.Header.Set(nativeClientIPHeader, "192.0.2.7")
	res, err := (&http.Client{Transport: transport}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native ingress did not stop")
	}
	if _, err := os.Lstat(nativeSocketPath(cfg.Package)); !os.IsNotExist(err) {
		t.Fatal("socket retained after stop", err)
	}
}

func TestDirectFallbackExplainsCertificateBeforeOpening(t *testing.T) {
	cfg := Config{Package: "App", Listen: Listen{HTTPS: 18443}}
	w := httptest.NewRecorder()
	EntryHandler(cfg).ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example:5001/webman/3rdparty/App/sso/open.cgi", nil))
	if w.Code != 200 || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), "certificate") || !strings.Contains(w.Body.String(), `href="https://nas.example:18443/"`) {
		t.Fatal("direct fallback opened without an explanation", w.Code, w.Body.String())
	}
}
