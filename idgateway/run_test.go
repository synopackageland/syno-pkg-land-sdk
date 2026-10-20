package idgateway

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReadDSMPorts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synoinfo.conf")
	os.WriteFile(path, []byte("hostname=\"nas\"\nadmin_port=\"5080\"\nsecure_admin_port=\"5443\"\n"), 0o644)
	if p := ReadDSMPorts(path); p != (DSMPorts{HTTP: 5080, HTTPS: 5443}) {
		t.Fatalf("%+v", p)
	}
	os.WriteFile(path, []byte("admin_port=\"x\"\n"), 0o644)
	if p := ReadDSMPorts(path); p != (DSMPorts{HTTP: 5000, HTTPS: 5001}) {
		t.Fatalf("defaults %+v", p)
	}
	if p := ReadDSMPorts(filepath.Join(t.TempDir(), "missing")); p != (DSMPorts{HTTP: 5000, HTTPS: 5001}) {
		t.Fatalf("missing %+v", p)
	}
}

func TestSelfSignedCertificateIsPrivateAndReused(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	a, err := loadCertificate(Config{StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := loadCertificate(Config{StateDir: state})
	if string(a.Certificate[0]) != string(b.Certificate[0]) {
		t.Fatal("certificate regenerated")
	}
	for _, f := range []string{"tls-cert.pem", "tls-key.pem"} {
		if info, err := os.Stat(filepath.Join(state, f)); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s %v", f, err)
		}
	}
}

func TestChildEnvCarriesIssuerAndSecrets(t *testing.T) {
	env := childEnv("http://127.0.0.1:18090", map[string]string{"paperless": "s1", "open-list.web": "s2"})
	want := []string{"SYNOPKGLAND_SSO_ISSUER=http://127.0.0.1:18090", "SYNOPKGLAND_SSO_SECRET_OPEN_LIST_WEB=s2", "SYNOPKGLAND_SSO_SECRET_PAPERLESS=s1"}
	if strings.Join(env, " ") != strings.Join(want, " ") {
		t.Fatalf("%v", env)
	}
}

func TestRunRefusesRoot(t *testing.T) {
	defer func(f func() int) { geteuid = f }(geteuid)
	geteuid = func() int { return 0 }
	path, _ := writeConfig(t, paperlessConfig)
	cfg, _ := LoadConfig(path)
	if err := Run(context.Background(), cfg, DSMPorts{HTTP: 5000, HTTPS: 5001}, nil); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("%v", err)
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestRunServesBothSchemesAndSupervisesTheApp(t *testing.T) {
	path, state := writeConfig(t, paperlessConfig)
	cfg, _ := LoadConfig(path)
	up := newUpstream(t, nil)
	cfg.Upstream = up.URL
	cfg.Listen = Listen{HTTP: freePort(t), HTTPS: freePort(t)}
	cfg.OIDC.IssuerPort = freePort(t)
	marker := filepath.Join(state, "child-saw")
	release := filepath.Join(state, "release")
	child := []string{"/bin/sh", "-c", `echo "$SYNOPKGLAND_SSO_ISSUER $SYNOPKGLAND_SSO_SECRET_PAPERLESS" > "$0"; while [ ! -e "$1" ]; do sleep 0.05; done`, marker, release}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), cfg, DSMPorts{HTTP: 5000, HTTPS: 5001}, child) }()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(url string) *http.Response {
		for i := 0; i < 100; i++ {
			if res, err := client.Get(url); err == nil {
				return res
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("no answer from %s", url)
		return nil
	}
	for _, u := range []string{"http://127.0.0.1:" + strconv.Itoa(cfg.Listen.HTTP) + "/", "https://127.0.0.1:" + strconv.Itoa(cfg.Listen.HTTPS) + "/"} {
		if res := get(u); res.StatusCode != 200 {
			t.Fatalf("%s: %d", u, res.StatusCode)
		}
	}
	if res := get(cfg.Issuer() + "/.well-known/openid-configuration"); res.StatusCode != 200 {
		t.Fatalf("issuer %d", res.StatusCode)
	}
	secret, _ := ClientSecret(state, "paperless")
	for i := 0; i < 100; i++ {
		if raw, _ := os.ReadFile(marker); string(raw) == cfg.Issuer()+" "+secret+"\n" {
			break
		} else if i == 99 {
			t.Fatalf("child env %q", raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	os.WriteFile(release, nil, 0o600)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "app exited") {
			t.Fatalf("run ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gateway outlived the app")
	}
}
