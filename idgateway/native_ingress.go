package idgateway

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// The DSM listener and its certificate belong to nginx. Its upstream is an
// encrypted Unix socket with a package-specific, mutually authenticated keypair.
// A public gateway listener never interprets this header as client identity.
const nativeClientIPHeader = "X-Synopkgland-Client-IP"

func nativeSocketPath(pkg string) string { return "/tmp/synopkgland-gateway-" + pkg + ".sock" }

func nativePublicPort(cfg Config) int {
	if cfg.Listen.HTTPS != 0 {
		return cfg.Listen.HTTPS
	}
	return cfg.Listen.HTTP
}

func nativeTLSConfig(cfg Config) (*tls.Config, error) {
	cert, err := readPrivate(filepath.Join(cfg.StateDir, "native-cert.pem"))
	if err != nil {
		return nil, fmt.Errorf("native ingress certificate: %w", err)
	}
	key, err := readPrivate(filepath.Join(cfg.StateDir, "native-key.pem"))
	if err != nil {
		return nil, fmt.Errorf("native ingress key: %w", err)
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	ca := x509.NewCertPool()
	if !ca.AppendCertsFromPEM(cert) {
		return nil, errors.New("invalid native ingress trust anchor")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: ca}, nil
}

func nativeListener(cfg Config) (net.Listener, error) {
	tlsConfig, err := nativeTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	path := nativeSocketPath(cfg.Package)
	if info, err := os.Lstat(path); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if info.Mode()&os.ModeSocket == 0 || !ok || stat.Uid != uint32(os.Getuid()) {
			return nil, errors.New("native ingress socket path is not an owned socket")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// nginx's http worker must traverse the socket. The socket carries no trust:
	// another package can connect but cannot complete the mutual TLS handshake.
	if err := os.Chmod(path, 0666); err != nil {
		l.Close()
		return nil, err
	}
	return tls.NewListener(l, tlsConfig), nil
}

func nativeIngressHandler(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			http.Error(w, "authenticated DSM ingress required", http.StatusForbidden)
			return
		}
		if r.URL.Path == PingPath {
			servePackagePing(w, r, cfg.Package)
			return
		}
		authority, err := url.Parse(requestOrigin(r))
		port := ""
		if err == nil {
			port = authority.Port()
			if port == "" {
				port = "443"
			}
		}
		if err != nil || port != strconv.Itoa(nativePublicPort(cfg)) || !validRequestAuthority(r) {
			http.Error(w, "invalid ingress authority", http.StatusBadRequest)
			return
		}
		values := r.Header.Values(nativeClientIPHeader)
		if len(values) != 1 || net.ParseIP(values[0]) == nil {
			http.Error(w, "invalid ingress client address", http.StatusBadRequest)
			return
		}
		request := r.Clone(r.Context())
		request.RemoteAddr = net.JoinHostPort(values[0], "0")
		request.Header.Del(nativeClientIPHeader)
		next.ServeHTTP(w, request)
	})
}
