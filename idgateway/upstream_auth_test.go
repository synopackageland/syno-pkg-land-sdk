package idgateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpstreamAuthDeclaration(t *testing.T) {
	for _, prefix := range []string{"", "/", "rest", "/rest/*", "/rest/**", "/a/../rest", "/a/./rest", "/%2e%2e/rest", "/rest%2fadmin", "/rest//api", "/rest\\api", "/rest?q=x", "/rest#x", "/rest api", "/_sso", "/_sso/authorize"} {
		t.Run(prefix, func(t *testing.T) {
			if err := validateUpstreamAuth([]string{prefix}, nil, nil); err == nil {
				t.Fatalf("accepted unsafe prefix %q", prefix)
			}
		})
	}
	for _, paths := range [][]string{{"/rest", "/rest/"}, {"/rest", "/rest/api"}} {
		if err := validateUpstreamAuth(paths, nil, nil); err == nil {
			t.Fatalf("accepted overlapping prefixes: %v", paths)
		}
	}
	if err := validateUpstreamAuth([]string{"/rest/", "/share"}, []string{"/rest/v1"}, map[string]OptionalPassthroughRule{"mobile": {Prefixes: []string{"/rest/mobile"}}}); err != nil {
		t.Fatal(err)
	}
	for _, passthrough := range []string{"/", "/restore", "/api", "/rest/../api", "/rest%2fapi"} {
		if err := validateUpstreamAuth([]string{"/rest"}, []string{passthrough}, nil); err == nil {
			t.Fatalf("accepted uncovered passthrough %q", passthrough)
		}
	}
}

func TestUpstreamAuthPathsPreserveCredentialsAndEntryFloor(t *testing.T) {
	for _, mode := range []Mode{ModeNone, ModeHeader, ModeToken, ModeOIDC} {
		t.Run(string(mode), func(t *testing.T) {
			up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != "native upload" || r.URL.RequestURI() != "/rest/upload?token=own%2Ftoken" {
					t.Errorf("request changed: %s %q", r.URL.RequestURI(), body)
				}
				if r.Header.Get("Authorization") != "Bearer native-token" || r.Header.Get("Cookie") != "native=session" {
					t.Errorf("native credentials changed: %v", r.Header)
				}
				for _, h := range []string{"Remote-User", "Remote_User", "Remote-Groups", "X-Syno-User", "X-Syno-Admin", "X-Syno-Token", "X-App-Identity"} {
					if r.Header.Get(h) != "" {
						t.Errorf("trusted identity leaked: %s", h)
					}
				}
			})
			f, c := newFront(t, mode, up, func(cfg *FrontConfig) {
				cfg.Entry = "admin-only"
				cfg.Passthrough = []string{"/rest"}
				cfg.UpstreamAuthPaths = []string{"/rest", "/share/"}
				cfg.Headers.Name = "X-App-Identity"
				cfg.Tokens = &staticTokens{}
				cfg.Membership = &changingMembership{groups: []string{"users"}}
			})
			f.sessions["ordinary"] = session{subject: alice, ip: "192.0.2.7", expires: c.now().Add(time.Minute)}
			r := httptest.NewRequest("POST", "https://app.example/rest/upload?token=own%2Ftoken", strings.NewReader("native upload"))
			r.RemoteAddr = "192.0.2.7:4444"
			r.Header.Set("Authorization", "Bearer native-token")
			r.Header.Set("Cookie", "id=dsm; did=device; spl_session_fixture=sdk; synopkgland_sso_App=ordinary; native=session")
			for _, h := range []string{"Remote-User", "Remote_User", "Remote-Groups", "X-Syno-User", "X-Syno-Admin", "X-Syno-Token", "X-App-Identity"} {
				r.Header.Set(h, "forged")
			}
			w := httptest.NewRecorder()
			f.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("upstream-auth request denied: %d %s", w.Code, w.Body)
			}
			b := newBrowser(t, f)
			for _, path := range []string{"/", "/restore", "/REST", "/upload", "/socket"} {
				for _, headers := range [][]string{nil, {"Connection", "Upgrade", "Upgrade", "websocket"}} {
					if w := b.do("POST", "https://app.example"+path, append([]string{"Origin", "https://app.example"}, headers...)...); w.Code != http.StatusUnauthorized {
						t.Fatalf("unlisted %s bypassed entry: %d %s", path, w.Code, w.Body)
					}
				}
			}
			b.cookies[f.cookie] = "ordinary"
			if w := b.do("GET", "https://app.example/upload"); w.Code != http.StatusForbidden {
				t.Fatalf("ordinary user bypassed administrator entry: %d", w.Code)
			}
			up.mu.Lock()
			defer up.mu.Unlock()
			if up.hits != 1 {
				t.Fatalf("protected traffic reached upstream: %d", up.hits)
			}
		})
	}
}

func TestUpstreamAuthPreservesNativeProtocolAndResponses(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		for name, want := range map[string]string{"Origin": "https://client.example", "If-None-Match": "native-etag", "Range": "bytes=1-5"} {
			if r.Header.Get(name) != want {
				t.Errorf("%s changed: %q", name, r.Header.Get(name))
			}
		}
		w.Header().Set("Location", issuer+"/authorize?client_id=native")
		w.Header().Add("Set-Cookie", "native=session; Path=/")
		w.Header().Add("Set-Cookie", "spl_session_forged=sdk; Path=/")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "upstream refusal")
	})
	f, _ := newFront(t, ModeOIDC, up, func(cfg *FrontConfig) {
		cfg.Entry = "admin-only"
		cfg.Passthrough = nil
		cfg.UpstreamAuthPaths = []string{"/api"}
		cfg.RewriteJSON = []JSONRewrite{{Method: "POST", Path: "/api", Field: "location"}}
		cfg.Redirects = map[string]string{"/api": "/login"}
	})
	w := newBrowser(t, f).do("POST", "https://app.example/api", "Origin", "https://client.example", "If-None-Match", "native-etag", "Range", "bytes=1-5")
	if w.Code != http.StatusUnauthorized || w.Body.String() != "upstream refusal" || w.Header().Get("Location") != issuer+"/authorize?client_id=native" {
		t.Fatalf("upstream response changed: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if cookies := w.Header().Values("Set-Cookie"); len(cookies) != 1 || cookies[0] != "native=session; Path=/" {
		t.Fatalf("cookie isolation failed: %v", cookies)
	}
}

func TestUpstreamAuthPathNormalization(t *testing.T) {
	f, _ := newFront(t, ModeNone, newUpstream(t, nil), func(cfg *FrontConfig) {
		cfg.Entry = "admin-only"
		cfg.Passthrough = nil
		cfg.UpstreamAuthPaths = []string{"/rest/", "/share"}
	})
	for path, status := range map[string]int{
		"/rest": 200, "/rest/": 200, "/rest/items": 200, "/r%65st/items": 200, "/share/key": 200,
		"/restore": 401, "/REST": 401, "/restored/items": 401, "/": 401,
		"/rest/../admin": 400, "/rest/%2e%2e/admin": 400, "/rest/%252e%252e/admin": 400,
		"/rest//admin": 400, "//rest": 400, "/rest%2fadmin": 400, "/rest%2Fadmin": 400,
		"/rest/%5cadmin": 400, "/rest/%255cadmin": 400, "/rest/%252fadmin": 400,
	} {
		if w := newBrowser(t, f).do("GET", "https://app.example"+path); w.Code != status {
			t.Errorf("%s: got %d, want %d", path, w.Code, status)
		}
	}
}

func TestUpstreamAuthKeepsReadinessAndComposeGates(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		up := newUpstream(t, nil)
		f, _ := newFront(t, ModeNone, up, func(cfg *FrontConfig) {
			cfg.Entry = "admin-only"
			cfg.Passthrough = nil
			cfg.UpstreamAuthPaths = []string{"/rest"}
			cfg.ComposeBlocked = blocked
			cfg.Ready = func(context.Context) bool { return false }
		})
		want := http.StatusServiceUnavailable
		if blocked {
			want = http.StatusUnauthorized
		}
		if w := newBrowser(t, f).do("GET", "https://app.example/rest"); w.Code != want {
			t.Fatalf("blocked=%v: %d %s", blocked, w.Code, w.Body)
		}
		up.mu.Lock()
		hits := up.hits
		up.mu.Unlock()
		if hits != 0 {
			t.Fatal("unready/blocked upstream received request")
		}
	}
}

func TestUpstreamAuthPolicyBindingInCompiledBinary(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(strings.ReplaceAll(paperlessConfig, "STATE", "/state")), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Entry, cfg.Access = "admin-only", "dsm-users"
	cfg.UpstreamAuthPaths = []string{"/rest", "/share"}
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "github.com/synopackageland/syno-pkg-land-sdk"
	mod := fmt.Sprintf("module gatewayprobe\n\ngo 1.25.0\nrequire %s v0.0.0\nreplace %s => %q\n", module, module, filepath.ToSlash(filepath.Dir(cwd)))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	const source = `package main
import ("fmt"; "os"; "github.com/synopackageland/syno-pkg-land-sdk/idgateway")
func main(){_,err:=idgateway.LoadConfig(os.Args[1]);if err!=nil{fmt.Print(err);os.Exit(1)};fmt.Print("admitted config")}`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "probe")
	cmd := exec.Command("go", "build", "-mod=mod", "-buildvcs=false", "-ldflags", "-X "+module+"/idgateway.expectedEntryPolicyDigest="+cfg.EntryPolicyDigest(), "-o", binary, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, edit := range []string{"unchanged", "entry", "paths", "public", "passthrough", "optional"} {
		c := cfg
		switch edit {
		case "entry":
			c.Entry = ""
		case "paths":
			c.UpstreamAuthPaths = []string{"/rest", "/share", "/api"}
		case "public":
			c.Access = "public"
		case "passthrough":
			c.Passthrough = []string{"/rest"}
		case "optional":
			c.OptionalPassthrough = map[string]OptionalPassthroughRule{"mobile": {Prefixes: []string{"/rest/mobile"}}}
		}
		raw, _ := json.Marshal(c)
		config := filepath.Join(dir, "gateway.json")
		if err := os.WriteFile(config, raw, 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(binary, config).CombinedOutput()
		if edit == "unchanged" {
			if err != nil || string(out) != "admitted config" {
				t.Fatalf("bound config: %v %s", err, out)
			}
		} else if err == nil || !strings.Contains(string(out), "gateway entry policy does not match package build policy") {
			t.Fatalf("%s bypassed packaged policy: %v %s", edit, err, out)
		}
	}
}

func TestUpstreamAuthOptionalPathsRemainDefaultOff(t *testing.T) {
	dir := t.TempDir()
	f, _ := newFront(t, ModeNone, newUpstream(t, nil), func(cfg *FrontConfig) {
		cfg.Entry = "admin-only"
		cfg.Passthrough = nil
		cfg.UpstreamAuthPaths = []string{"/rest"}
		cfg.OptionalPassthrough = map[string]OptionalPassthroughRule{"mobile": {Prefixes: []string{"/rest/mobile"}}}
		cfg.StateDir = dir
	})
	for _, enabled := range []bool{false, true, false} {
		if err := os.WriteFile(optionalStatePath(dir, "mobile"), []byte(fmt.Sprint(enabled)), 0600); err != nil {
			t.Fatal(err)
		}
		f.ReloadOptionalPassthrough()
		want := http.StatusForbidden
		if enabled {
			want = http.StatusOK
		}
		if w := newBrowser(t, f).do("GET", "https://app.example/rest/mobile"); w.Code != want {
			t.Fatalf("enabled=%v: %d %s", enabled, w.Code, w.Body)
		}
	}
}

func TestUpstreamAuthWebSocketUsesOnlyNativeCredentials(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer native" || r.Header.Get("Remote-User") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("upgrade identity leaked or native credential changed: %v", r.Header)
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		if err := rw.Flush(); err != nil {
			return
		}
		io.Copy(conn, rw)
	})
	f, _ := newFront(t, ModeHeader, up, func(cfg *FrontConfig) {
		cfg.Entry = "admin-only"
		cfg.Passthrough = nil
		cfg.UpstreamAuthPaths = []string{"/notifications"}
	})
	srv := httptest.NewServer(f)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET /notifications/hub HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nAuthorization: Bearer native\r\nRemote-User: forged\r\nCookie: id=dsm; spl_session_fixture=sdk\r\n\r\n", addr)
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("native upgrade denied: %v %v", res, err)
	}
	if _, err := conn.Write([]byte("native")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "native" {
		t.Fatalf("native stream failed: %q %v", buf, err)
	}
}
