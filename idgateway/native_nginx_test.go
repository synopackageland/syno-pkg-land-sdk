package idgateway

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The cross-repository gate supplies the CLI's actual generated nginx config.
// Normal SDK gates need no nginx installation or second repository checkout.
func TestNativeNginxIntegration(t *testing.T) {
	generated := os.Getenv("SYNOPKGLAND_NATIVE_NGINX_CONFIG")
	if generated == "" {
		t.Skip("cross-repository nginx fixture not supplied")
	}
	if _, err := exec.LookPath("nginx"); err != nil {
		t.Fatal(err)
	}
	state := "/var/packages/SampleApp/var/sso"
	nativeTestCertificate(t, state)
	cfg := Config{Package: "SampleApp", StateDir: state, NativeIngress: true, Listen: Listen{HTTP: 18080, HTTPS: 18443}}
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket" {
			w.WriteHeader(200)
			return
		}
		c, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		rw.Flush()
		io.Copy(c, rw)
	})
	membership := &liveDirectory{}
	membership.allowed.Store(true)
	f, _ := newFront(t, ModeHeader, up, func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = membership })
	f.sessions["native-session"] = session{subject: alice, ip: "127.0.0.1", expires: f.cfg.Now().Add(time.Hour)}
	l, err := nativeListener(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: nativeIngressHandler(cfg, f)}
	go srv.Serve(l)
	defer srv.Close()
	root := t.TempDir()
	publicA := nativeTestCertificate(t, filepath.Join(root, "a"))
	publicB := nativeTestCertificate(t, filepath.Join(root, "b"))
	active := filepath.Join(root, "active")
	os.Mkdir(active, 0700)
	activate := func(from string) {
		for _, name := range []string{"native-cert.pem", "native-key.pem"} {
			data, err := os.ReadFile(filepath.Join(root, from, name))
			if err != nil {
				t.Fatal(err)
			}
			// Replace files atomically, as certificate deployment does. Newer nginx
			// can retain SSL objects when both inode and second-resolution mtime
			// are unchanged across a reload.
			path := filepath.Join(active, name)
			if err := os.WriteFile(path+".new", data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+".new", path); err != nil {
				t.Fatal(err)
			}
		}
	}
	activate("a")
	conf := fmt.Sprintf(`pid %s/nginx.pid;
error_log %s/error.log error;
events { worker_connections 128; }
http {
 access_log off;
 client_body_temp_path %s/body;
 proxy_temp_path %s/proxy;
 fastcgi_temp_path %s/fastcgi;
 uwsgi_temp_path %s/uwsgi;
 scgi_temp_path %s/scgi;
 ssl_certificate %s/native-cert.pem;
 ssl_certificate_key %s/native-key.pem;
 include %s;
}
`, root, root, root, root, root, root, root, active, active, generated)
	confPath := filepath.Join(root, "nginx.conf")
	os.WriteFile(confPath, []byte(conf), 0600)
	var logs bytes.Buffer
	nginx := exec.Command("nginx", "-p", root, "-c", confPath, "-g", "daemon off;")
	nginx.Stderr = &logs
	if err := nginx.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		exec.Command("nginx", "-p", root, "-c", confPath, "-s", "quit").Run()
		nginx.Wait()
		if t.Failed() {
			data, _ := os.ReadFile(filepath.Join(root, "error.log"))
			t.Log(logs.String(), string(data))
		}
	}()
	ca := x509.NewCertPool()
	for _, pair := range []tls.Certificate{publicA, publicB} {
		c, _ := x509.ParseCertificate(pair.Certificate[0])
		ca.AddCert(c)
	}
	publicTLS := &tls.Config{RootCAs: ca, ServerName: "gateway.invalid", MinVersion: tls.VersionTLS12}
	request := func(method, path, origin string) (*http.Response, error) {
		transport := &http.Transport{TLSClientConfig: publicTLS, DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		r, _ := http.NewRequest(method, "https://127.0.0.1:18443"+path, nil)
		r.Host = "nas.example:18443"
		r.Header.Set("Origin", origin)
		r.Header.Set("Accept", "text/html")
		r.Header.Set("Sec-Fetch-Mode", "navigate")
		r.Header.Set(nativeClientIPHeader, "192.0.2.99") // nginx must overwrite it.
		r.AddCookie(&http.Cookie{Name: f.cookie, Value: "native-session"})
		return (&http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(r)
	}
	var response *http.Response
	for i := 0; i < 100; i++ {
		response, err = request("GET", "/", "https://nas.example:18443")
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("native request or client address binding failed", response.StatusCode)
	}
	before := sha256.Sum256(response.TLS.PeerCertificates[0].Raw)
	response, err = request("POST", "/update", "https://nas.example:5001")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-origin mutation passed nginx", response.StatusCode)
	}
	response, err = request("POST", "/update", "https://nas.example:18443")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("native mutation failed", response.StatusCode)
	}
	conn, err := tls.Dial("tcp", "127.0.0.1:18443", publicTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(conn, "GET /socket HTTP/1.1\r\nHost: nas.example:18443\r\nOrigin: https://nas.example:18443\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nCookie: %s=native-session\r\n\r\n", f.cookie)
	br := bufio.NewReader(conn)
	response, err = http.ReadResponse(br, nil)
	if err != nil || response.StatusCode != 101 {
		t.Fatal("nginx WebSocket upgrade failed", response, err)
	}
	conn.Write([]byte("frames"))
	echo := make([]byte, 6)
	if _, err := io.ReadFull(br, echo); err != nil || string(echo) != "frames" {
		t.Fatal("nginx WebSocket forwarding failed", string(echo), err)
	}
	conn.Close()
	activate("b")
	if out, err := exec.Command("nginx", "-p", root, "-c", confPath, "-s", "reload").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	rotated := false
	for i := 0; i < 100; i++ {
		response, err = request("GET", "/", "https://nas.example:18443")
		if err == nil {
			response.Body.Close()
			if sha256.Sum256(response.TLS.PeerCertificates[0].Raw) != before {
				rotated = response.StatusCode == 200
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !rotated {
		t.Fatal("native server cached the old inherited public certificate")
	}
	membership.allowed.Store(false)
	response, err = request("GET", "/", "https://nas.example:18443")
	if err != nil {
		t.Fatal(err)
	}
	denial, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 403 || !strings.Contains(string(denial), "requires a DSM administrator") || strings.Contains(string(denial), "gateway.retry") {
		t.Fatal("native ingress bypassed admission or offered a forbidden retry", response.StatusCode, string(denial))
	}
	for _, path := range []string{filepath.Join(state, "native-key.pem"), filepath.Join(state, "native-cert.pem")} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private ingress credential mode changed", path, err)
		}
	}
	if strings.Contains(conf, "/certificate/system/") {
		t.Fatal("test copied a DSM private key path")
	}
}
