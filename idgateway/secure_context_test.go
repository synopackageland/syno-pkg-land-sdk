package idgateway

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSecureContextHTTPGuidance(t *testing.T) {
	for _, mode := range []Mode{ModeNone, ModeHeader, ModeToken, ModeOIDC} {
		t.Run(string(mode), func(t *testing.T) {
			up := newUpstream(t, nil)
			f, _ := newFront(t, mode, up, func(c *FrontConfig) {
				c.SecureContext = "required"
				c.Tokens = &staticTokens{}
				c.PublicListen = Listen{HTTPS: 28443}
			})
			r := httptest.NewRequest("GET", "http://app.example:28080/library?token=private", nil)
			r.Header.Set("Accept", "text/html")
			r.Header.Set("Accept-Language", "zh-TW")
			r.Header.Set("Sec-Fetch-Mode", "navigate")
			r.Header.Set("Sec-Fetch-Dest", "document")
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set(nativeSchemeHeader, "https")
			w := httptest.NewRecorder()
			f.ServeHTTP(w, r)
			body := w.Body.String()
			if w.Code != 403 || !strings.Contains(body, "需要 HTTPS") || !strings.Contains(body, `href="https://app.example:28443/library"`) || strings.Contains(body, "private") || up.hits != 0 {
				t.Fatalf("guidance: %d %s; upstream hits %d", w.Code, body, up.hits)
			}
			if w.Header().Get("Location") != "" || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), cspHash(secureContextStyle)) {
				t.Fatalf("unexpected headers: %v", w.Header())
			}
		})
	}
}

func TestSecureContextNormalRoutes(t *testing.T) {
	for _, tc := range []struct{ policy, scheme string }{{"required", "https"}, {"", "http"}, {"", "https"}} {
		t.Run(tc.policy+tc.scheme, func(t *testing.T) {
			f, _ := newFront(t, ModeNone, newUpstream(t, nil), func(c *FrontConfig) { c.SecureContext, c.Access = tc.policy, "public" })
			r := httptest.NewRequest("GET", tc.scheme+"://app.example/library", nil)
			r.Header.Set("Accept", "text/html")
			w := httptest.NewRecorder()
			f.ServeHTTP(w, r)
			if w.Code != 200 || w.Body.String() != "app" {
				t.Fatalf("normal route changed: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestSecureContextResourceRefusals(t *testing.T) {
	for _, tc := range []struct{ method, accept, mode, dest string }{
		{"GET", "application/json", "cors", "empty"},
		{"GET", "*/*", "no-cors", "script"},
		{"POST", "text/html", "navigate", "document"},
		{"GET", "text/html", "navigate", "iframe"},
		{"HEAD", "text/html", "", ""},
	} {
		t.Run(tc.method+tc.dest, func(t *testing.T) {
			up := newUpstream(t, nil)
			f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.SecureContext = "required" })
			r := httptest.NewRequest(tc.method, "http://app.example/share/file", nil)
			r.Header.Set("Accept", tc.accept)
			r.Header.Set("Sec-Fetch-Mode", tc.mode)
			r.Header.Set("Sec-Fetch-Dest", tc.dest)
			w := httptest.NewRecorder()
			f.ServeHTTP(w, r)
			var body map[string]string
			if w.Code != 403 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["error"] != "https_required" || up.hits != 0 {
				t.Fatalf("resource: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestSecureContextIngressScheme(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		up := newUpstream(t, nil)
		f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) {
			c.SecureContext, c.Access, c.IngressPath, c.DSMHTTPSPort = "required", "public", "/synopkgland/App/", 7443
		})
		r := httptest.NewRequest("GET", "https://app.example:7080/synopkgland/App/", nil)
		// The internal transport is TLS even when the browser uses HTTP.
		r.TLS = &tls.ConnectionState{}
		r = r.WithContext(context.WithValue(r.Context(), ingressSchemeKey{}, scheme))
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		if scheme == "http" {
			if w.Code != 403 || !strings.Contains(w.Body.String(), `href="https://app.example:7443/webman/?launchApp=SYNO.SDS.App.Instance"`) {
				t.Fatalf("HTTP ingress: %d %s", w.Code, w.Body)
			}
		} else if w.Code != 200 || w.Body.String() != "app" {
			t.Fatalf("HTTPS ingress: %d %s", w.Code, w.Body)
		}
	}
}

func TestSecureContextUnknownPortAndAuthority(t *testing.T) {
	f, _ := newFront(t, ModeNone, newUpstream(t, nil), func(c *FrontConfig) {
		c.SecureContext, c.IngressPath = "required", "/synopkgland/App/"
		// Legacy authentication fallback and a declared App listener cannot
		// establish DSM's HTTPS port for a path ingress.
		c.DSMPorts.HTTPS, c.PublicListen.HTTPS = 5001, 28443
	})
	r := httptest.NewRequest("GET", "http://app.example:7080/synopkgland/App/", nil)
	r.Header.Set("Accept", "text/html")
	r.Header.Set("X-Forwarded-Port", "443")
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	if w.Code != 403 || strings.Contains(w.Body.String(), "href=") || !strings.Contains(w.Body.String(), "gateway.https_unknown") {
		t.Fatalf("unknown port: %d %s", w.Code, w.Body)
	}
	r.Host = "bad.example@other.example"
	w = httptest.NewRecorder()
	f.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("invalid authority: %d", w.Code)
	}
}

func TestDeclaredDSMHTTPSPort(t *testing.T) {
	for _, tc := range []struct {
		name, dsm, synoinfo string
		want                int
	}{
		{"DSM only", `{"port":7080,"ssl":{"port":7443,"redirect":false}}`, "", 7443},
		{"DSM default declaration", `{"port":5000,"ssl":{"port":5001}}`, "", 5001},
		{"synoinfo only", "", "secure_admin_port=\"7443\"\n", 7443},
		{"DSM takes precedence", `{"ssl":{"port":8443}}`, "secure_admin_port=\"7443\"\n", 8443},
		{"both missing", "", "", 0},
		{"HTTP only synoinfo", "", "admin_port=\"7080\"\n", 0},
		{"malformed DSM fallback", `{"ssl":`, "secure_admin_port=\"7443\"\n", 7443},
		{"malformed DSM alone", `{"ssl":`, "", 0},
		{"trailing JSON fallback", `{"ssl":{"port":8443}} {}`, "secure_admin_port=\"7443\"\n", 7443},
		{"invalid port fallback", `{"ssl":{"port":"8443"}}`, "secure_admin_port=\"7443\"\n", 7443},
		{"missing port fallback", `{"ssl":{}}`, "secure_admin_port=\"7443\"\n", 7443},
		{"negative port", `{"ssl":{"port":-1}}`, "", 0},
		{"large port", `{"ssl":{"port":65536}}`, "", 0},
		{"fractional port", `{"ssl":{"port":7443.5}}`, "", 0},
		{"HTTPS section absent", `{"port":7080}`, "secure_admin_port=\"7443\"\n", 0},
		{"HTTPS section null", `{"ssl":null}`, "secure_admin_port=\"7443\"\n", 0},
		{"HTTPS section false", `{"ssl":false}`, "secure_admin_port=\"7443\"\n", 0},
		{"HTTPS zero port", `{"ssl":{"port":0}}`, "secure_admin_port=\"7443\"\n", 0},
		{"null document fallback", `null`, "secure_admin_port=\"7443\"\n", 7443},
		{"bad synoinfo port", "", "secure_admin_port=\"bad\"\n", 0},
		{"zero synoinfo port", "", "secure_admin_port=\"0\"\n", 0},
		{"large synoinfo port", "", "secure_admin_port=\"65536\"\n", 0},
		{"duplicate synoinfo port", "", "secure_admin_port=\"443\"\nsecure_admin_port=\"7443\"\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dsm, synoinfo := filepath.Join(dir, "DSM.json"), filepath.Join(dir, "synoinfo.conf")
			for path, data := range map[string]string{dsm: tc.dsm, synoinfo: tc.synoinfo} {
				if data != "" {
					if err := os.WriteFile(path, []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			port := declaredDSMHTTPSPort(dsm, synoinfo)
			if port != tc.want {
				t.Fatalf("HTTPS port: got %d, want %d", port, tc.want)
			}
			f, _ := newFront(t, ModeNone, newUpstream(t, nil), func(c *FrontConfig) {
				c.SecureContext, c.IngressPath, c.DSMHTTPSPort = "required", "/synopkgland/App/", port
			})
			r := httptest.NewRequest("GET", "http://app.example:7080/synopkgland/App/?token=private", nil)
			r.Header.Set("Accept", "text/html")
			w := httptest.NewRecorder()
			f.ServeHTTP(w, r)
			body := w.Body.String()
			if w.Code != 403 || strings.Contains(body, "private") {
				t.Fatalf("guidance: %d %s", w.Code, body)
			}
			if tc.want == 0 {
				if strings.Contains(body, "href=") || !strings.Contains(body, "gateway.https_unknown") {
					t.Fatalf("unknown or disabled HTTPS produced a link: %s", body)
				}
			} else if !strings.Contains(body, `href="https://app.example:`+strconv.Itoa(tc.want)+`/webman/?launchApp=SYNO.SDS.App.Instance"`) {
				t.Fatalf("missing discovered HTTPS link: %s", body)
			}
		})
	}
}

func TestSecureContextCatalogs(t *testing.T) {
	for _, locale := range denialLocales {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", locale)
		_, messages := denialMessages(r)
		raw, err := os.ReadFile("../ts/i18n/identity/" + locale + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var shared struct {
			Title  string            `json:"httpsTitle"`
			Errors map[string]string `json:"errors"`
		}
		if err := json.Unmarshal(raw, &shared); err != nil {
			t.Fatal(err)
		}
		if messages["https_title"] != shared.Title || messages["https_required"] != shared.Errors["https_required"] {
			t.Fatalf("shared copy drift: %s", locale)
		}
		for _, key := range []string{"https_title", "https_required", "https_open", "https_unknown"} {
			value := messages[key]
			if value == "" || strings.HasSuffix(value, ".") || strings.HasSuffix(value, "。") || strings.Contains(value, "…") || strings.Contains(value, "...") {
				t.Fatalf("invalid copy %s/%s: %q", locale, key, value)
			}
		}
	}
}

func TestSecureContextConfig(t *testing.T) {
	if validateSecureContext("") != nil || validateSecureContext("required") != nil || validateSecureContext("optional") == nil {
		t.Fatal("invalid policy validation")
	}
	file := filepath.Join(t.TempDir(), "gateway.json")
	for _, value := range []string{`true`, `1`, `"optional"`} {
		raw := strings.Replace(paperlessConfig, `"package":`, `"secure_context":`+value+`,"package":`, 1)
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(file); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestSecureContextBuildConfig(t *testing.T) {
	cfg := Config{Package: "App", Mode: ModeNone, Access: "public", SecureContext: "required", StateDir: t.TempDir(), Upstream: "http://127.0.0.1:8000", AuthorizeCGI: "/webman/3rdparty/App/sso/authorize.cgi", Listen: Listen{HTTP: 28080, HTTPS: 28443}}
	gw, err := Build(cfg, DSMPorts{HTTP: 5000, HTTPS: 5001})
	if err != nil {
		t.Fatal(err)
	}
	if gw.Front.cfg.SecureContext != "required" || gw.Front.cfg.PublicListen.HTTPS != 28443 {
		t.Fatal("lost secure context configuration")
	}
	r := httptest.NewRequest("GET", "http://app.example:28080/", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	gw.Front.ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), `href="https://app.example:28443/"`) {
		t.Fatalf("configured guidance: %d %s", w.Code, w.Body)
	}
}
