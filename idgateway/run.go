package idgateway

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ReadDSMPorts reads DSM's HTTP and HTTPS ports from synoinfo.conf
// (/etc/synoinfo.conf), defaulting to 5000 and 5001.
func ReadDSMPorts(path string) DSMPorts {
	ports := DSMPorts{HTTP: 5000, HTTPS: 5001}
	f, err := os.Open(path)
	if err != nil {
		return ports
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		key, value, _ := strings.Cut(lines.Text(), "=")
		port, err := strconv.Atoi(strings.Trim(value, `"`))
		if err != nil || !validPort(port, false) {
			continue
		}
		switch key {
		case "admin_port":
			ports.HTTP = port
		case "secure_admin_port":
			ports.HTTPS = port
		}
	}
	return ports
}

// loadCertificate returns the configured certificate, or a self-signed one
// kept in the state directory.
func loadCertificate(cfg Config) (tls.Certificate, error) {
	if cfg.TLS != nil {
		return tls.LoadX509KeyPair(cfg.TLS.Cert, cfg.TLS.Key)
	}
	certPath, keyPath := filepath.Join(cfg.StateDir, "tls-cert.pem"), filepath.Join(cfg.StateDir, "tls-key.pem")
	certPEM, err := readPrivate(certPath)
	if errors.Is(err, os.ErrNotExist) {
		var keyPEM []byte
		if certPEM, keyPEM, err = selfSigned(cfg.Package); err != nil {
			return tls.Certificate{}, err
		}
		if err = writePrivate(keyPath, keyPEM); err == nil {
			err = writePrivate(certPath, certPEM)
		}
	}
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := readPrivate(keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func selfSigned(name string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), nil
}

// childEnv tells the app the issuer and each client's secret, as
// SYNOPKGLAND_SSO_SECRET_<ID> with the id upper-cased and "-" and "." as "_".
func childEnv(issuer string, secrets map[string]string) []string {
	var env []string
	if issuer != "" {
		env = append(env, "SYNOPKGLAND_SSO_ISSUER="+issuer)
	}
	for id, secret := range secrets {
		name := strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToUpper(id))
		env = append(env, "SYNOPKGLAND_SSO_SECRET_"+name+"="+secret)
	}
	slices.Sort(env)
	return env
}

var geteuid = os.Geteuid

// AppStopTimeout bounds the app's shutdown after SIGTERM.
const AppStopTimeout = 20 * time.Second

// Run serves the gateway until ctx ends or, when app is given, until that
// supervised app process exits. It refuses to run as root.
func Run(ctx context.Context, cfg Config, ports DSMPorts, app []string) error {
	if geteuid() == 0 {
		return errors.New("refusing to run as root: run the gateway as the package account")
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	if socket := os.Getenv("SYNOPKGLAND_DAEMON_SOCKET"); socket != "" {
		cfg.Ready = daemonReadiness(socket, os.Getenv("SYNOPKGLAND_DAEMON_DIGEST"), cfg.operationBudget())
	}
	gw, err := Build(cfg, ports)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopReload, err := gw.Front.watchOptional(ctx)
	if err != nil {
		return err
	}
	defer stopReload()
	if gw.Deletions != nil {
		go func() {
			if err := gw.Deletions.Watch(ctx, log.Printf); err != nil {
				log.Printf("account deletions: not watching /etc, only the startup comparison ran: %v", err)
			}
		}()
	}
	// Keep-alive connections that send nothing are closed; request bodies and
	// responses stream, so there is no read or write deadline.
	const idleTimeout = 2 * time.Minute
	var servers []*http.Server
	errc := make(chan error, 4)
	serve := func(addr string, h http.Handler, cert *tls.Certificate) error {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: idleTimeout}
		if cert != nil {
			l = tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{*cert}, MinVersion: tls.VersionTLS12})
		}
		servers = append(servers, srv)
		go func() { errc <- srv.Serve(l) }()
		return nil
	}
	defer func() {
		for _, srv := range servers {
			srv.Close()
		}
	}()
	if gw.Provider != nil {
		if err := serve(strings.TrimPrefix(cfg.Issuer(), "http://"), gw.Provider, nil); err != nil {
			return err
		}
	}
	if cfg.NativeIngress {
		listener, err := nativeListener(cfg)
		if err != nil {
			return err
		}
		srv := &http.Server{Handler: nativeIngressHandler(cfg, gw.Front), ReadHeaderTimeout: 30 * time.Second, IdleTimeout: idleTimeout}
		servers = append(servers, srv)
		go func() { errc <- srv.Serve(listener) }()
	}
	if !cfg.NativeIngress && cfg.Listen.HTTP != 0 {
		if err := serve(":"+strconv.Itoa(cfg.Listen.HTTP), gw.Front, nil); err != nil {
			return err
		}
	}
	if !cfg.NativeIngress && cfg.Listen.HTTPS != 0 {
		cert, err := loadCertificate(cfg)
		if err != nil {
			return err
		}
		if err := serve(":"+strconv.Itoa(cfg.Listen.HTTPS), gw.Front, &cert); err != nil {
			return err
		}
	}
	if len(app) == 0 {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errc:
			return err
		}
	}
	cmd := exec.Command(app[0], app[1:]...)
	cmd.Env = append(os.Environ(), childEnv(cfg.Issuer(), gw.Secrets)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start app: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		return fmt.Errorf("app exited: %v", err)
	case err = <-errc:
	case <-ctx.Done():
	}
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(appStopBudget(cfg)):

		cmd.Process.Kill()
		select {
		case <-exited:
		case <-time.After(runtimebudget.Cleanup):
			return errors.New("app shutdown cleanup pending")
		}
	}
	return err
}

func appStopBudget(cfg Config) time.Duration {
	if cfg.StopSeconds > 0 && cfg.StopSeconds <= 3600 {
		return runtimebudget.Inner(runtimebudget.Inner(time.Duration(cfg.StopSeconds) * time.Second))
	}
	return AppStopTimeout
}

// Every upstream admission checks the package-private supervisor, including
// public and passthrough routes. Unknown and unavailable both deny ingress.
func daemonReadiness(socket, digest string, budget time.Duration) func(context.Context) bool {
	return func(ctx context.Context) bool {
		ctx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
		if err != nil {
			return false
		}
		defer conn.Close()
		if until, ok := ctx.Deadline(); ok {
			conn.SetDeadline(until)
		}
		if _, err = fmt.Fprintf(conn, "status %s\n", digest); err != nil {
			return false
		}
		line, err := bufio.NewReaderSize(conn, 256).ReadString('\n')
		return err == nil && line == "ok\n"
	}
}
