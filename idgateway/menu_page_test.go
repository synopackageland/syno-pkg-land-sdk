package idgateway

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type menuAuth struct {
	id    api.Identity
	err   error
	calls int
}

func (a *menuAuth) Authenticate(*http.Request) (api.Identity, error) {
	a.calls++
	return a.id, a.err
}

func openMenu(t *testing.T, cfg Config, src MenuSources, auth *menuAuth, connect bool, rawURL string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", rawURL, nil)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	var a api.Authenticator
	if auth != nil {
		a = auth
	}
	entryHandlerWithMenu(cfg, nil, nil, a, func() MenuSources { return src }, func() bool { return connect }).ServeHTTP(w, r)
	return w
}

func TestMenuLauncherKeepsLANBaseline(t *testing.T) {
	cfg, src := menuInputs()
	cfg.LaunchPath = "/#/home"
	for _, host := range []string{"192.0.2.10:5001", "nas.local:5001", "nas.example.ts.net:5002"} {
		auth := &menuAuth{}
		with := openMenu(t, cfg, src, auth, true, "https://"+host+"/webman/3rdparty/App/sso/open.cgi")
		without := openMenu(t, cfg, MenuSources{}, auth, true, "https://"+host+"/webman/3rdparty/App/sso/open.cgi")
		if with.Code != 302 || with.Code != without.Code || with.Body.String() != without.Body.String() || with.Header().Get("Location") != without.Header().Get("Location") || with.Header().Get("Location") != "https://"+hostname(host)+":18443/#/home" {
			t.Fatal(host, with.Code, with.Header(), without.Header())
		}
		if auth.calls != 0 {
			t.Fatal("baseline authenticated", host)
		}
	}
	// A forwarded host never selects a route.
	w := openMenu(t, cfg, src, &menuAuth{}, true, "https://192.0.2.10:5001/open.cgi", "X-Forwarded-Host", "dsm.example.com", "Forwarded", "host=dsm.example.com")
	if w.Code != 302 || w.Header().Get("Location") != "https://192.0.2.10:18443/#/home" {
		t.Fatal(w.Code, w.Header())
	}
}

func TestMenuLauncherProbesTrustedAddress(t *testing.T) {
	cfg, src := menuInputs()
	cfg.LaunchPath = "/#/home"
	user := &menuAuth{id: api.Identity{User: "alice"}}
	w := openMenu(t, cfg, src, user, true, "https://dsm.example.com/webman/3rdparty/App/sso/open.cgi")
	body, csp := w.Body.String(), w.Header().Get("Content-Security-Policy")
	sum := sha256.Sum256([]byte(menuProbeScript))
	for _, want := range []string{`data-ping="https://app.example.com/_spl/ping"`, `data-package="App"`, `data-target="https://app.example.com/#/home"`, `<noscript><p><a href="https://app.example.com/#/home">`, `<section class="box" id="unreachable" role="alert" aria-labelledby="unreachable-title" hidden>`, `<script>` + menuProbeScript + `</script>`} {
		if !strings.Contains(body, want) {
			t.Fatal("missing", want, body)
		}
	}
	if w.Code != 200 || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(csp, "script-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") || !strings.Contains(csp, "connect-src https://app.example.com;") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatal(w.Code, w.Header())
	}
	if strings.Contains(body, "launchApp=") || strings.Contains(body, "Cloudflare") {
		t.Fatal("ordinary user saw administrator details", body)
	}
	admin := &menuAuth{id: api.Identity{User: "admin", Admin: true}}
	w = openMenu(t, cfg, src, admin, true, "https://nas.example.ts.net:5001/webman/3rdparty/App/sso/open.cgi")
	if !strings.Contains(w.Body.String(), `data-target="https://nas.example.ts.net:18443/#/home"`) || !strings.Contains(w.Body.String(), `href="/?launchApp=SYNO.SDS.SynoPkgConnect.Instance&amp;launchParam=route%3D%252Fapps%252FApp%252Fprivate"`) {
		t.Fatal(w.Body)
	}
}

func TestMenuDeepLinksAcrossProvidersAndFallbackLinks(t *testing.T) {
	for _, provider := range []string{ProviderCloudflare, ProviderTailscale, ProviderWorkers, ProviderManual, ProviderCanonical} {
		t.Run(provider, func(t *testing.T) {
			cfg, src := menuInputs()
			cfg.LaunchPath = "/menu?view=files&sort=name#opened"
			host, origin := "dsm.example.com", "https://app.example.com"
			switch provider {
			case ProviderTailscale:
				host, origin = "nas.example.ts.net:5001", "https://nas.example.ts.net:18443"
			case ProviderWorkers:
				host, origin = "spl-dsm-1a2b.example.workers.dev", "https://app.example.workers.dev"
				src.Launch.Routes[2].Apps["App"] = LaunchApp{Origin: origin, Port: 18443, State: "active"}
			case ProviderManual, ProviderCanonical:
				src.State = ExternalState{Schema: 1, Revision: 1, DSMOrigin: "https://" + host, ManualOrigin: origin}
				cfg.External = &ExternalConfig{SingleOrigin: provider == ProviderCanonical}
				src.External = EffectiveExternal(cfg, src.State, src.Projection)
			}
			decision := MenuDestination(cfg, src, host, true)
			if decision.Kind != MenuRedirect || decision.Provider != provider {
				t.Fatal(decision)
			}
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				r := httptest.NewRequest(method, "https://"+host+"/open.cgi", strings.NewReader("SynoToken=valid-token"))
				r.Header.Set("X-SYNO-TOKEN", "valid-token")
				if method == http.MethodPost {
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					r.Header.Set("Origin", "https://"+host)
				}
				w := httptest.NewRecorder()
				entryHandlerWithMenu(cfg, nil, nil, &menuTokenAuth{}, func() MenuSources { return src }, func() bool { return true }).ServeHTTP(w, r)
				target := html.EscapeString(origin + cfg.LaunchPath)
				for _, want := range []string{`data-target="` + target + `"`, `<noscript><p><a href="` + target + `">`, `<a class="button" href="` + target + `">`} {
					if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
						t.Fatalf("%s missing %s: %d %s", method, want, w.Code, w.Body)
					}
				}
				if !strings.Contains(w.Body.String(), `data-ping="`+origin+PingPath+`"`) {
					t.Fatal("deep link must not be sent to ping", w.Body)
				}
			}
		})
	}
}

func TestMenuLauncherExplains(t *testing.T) {
	cfg, src := menuInputs()
	admin := &menuAuth{id: api.Identity{User: "admin", Admin: true}}
	user := &menuAuth{id: api.Identity{User: "alice"}}
	page := func(auth *menuAuth, connect bool, host string) string {
		w := openMenu(t, cfg, src, auth, connect, "https://"+host+"/open.cgi", "Accept-Language", "en")
		if w.Code != 200 || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "<script") || strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src") {
			t.Fatal(host, w.Code, w.Header(), w.Body)
		}
		return w.Body.String()
	}
	got := page(admin, true, "spl-dsm-1a2b.example.workers.dev")
	if !strings.Contains(got, `data-error-code="menu_not_enabled"`) || !strings.Contains(got, "through workers.dev, but this App has no workers.dev address yet") || !strings.Contains(got, "launchParam=route%3D%252Fapps%252FApp%252Fworkers") {
		t.Fatal(got)
	}
	if got := page(admin, false, "spl-dsm-1a2b.example.workers.dev"); strings.Contains(got, "launchApp=") || !strings.Contains(got, "Install NAS Remote Connect from Package Center") {
		t.Fatal("Connect missing", got)
	}
	if got := page(admin, true, "example-id.relay.quickconnect.to"); !strings.Contains(got, `data-error-code="menu_quickconnect"`) || !strings.Contains(got, "launchParam=route%3D%252Fapps%252FApp\"") {
		t.Fatal("QuickConnect", got)
	}
	// Ordinary users learn nothing about providers, approvals or addresses.
	for _, host := range []string{"spl-dsm-1a2b.example.workers.dev", "example-id.relay.quickconnect.to"} {
		got := page(user, true, host)
		if !strings.Contains(got, `data-error-code="menu_unavailable"`) || strings.Contains(got, "workers.dev") && !strings.Contains(host, "workers") || strings.Contains(got, "launchApp=") || strings.Contains(got, "example.com") {
			t.Fatal(host, got)
		}
		if strings.Contains(got, "has no") || strings.Contains(got, "QuickConnect reaches") {
			t.Fatal("reason leaked", got)
		}
	}
	src.Launch.Routes[0].Apps["App"] = LaunchApp{Origin: "https://app.example.com", Port: 18443, State: "paused"}
	if got := page(admin, true, "dsm.example.com"); !strings.Contains(got, "Cloudflare address is paused") || strings.Contains(got, "app.example.com") {
		t.Fatal("paused", got)
	}
	cfg.External = &ExternalConfig{SingleOrigin: true}
	if got := page(admin, true, "nas.example.ts.net:5001"); !strings.Contains(got, `data-error-code="menu_single_local"`) || !strings.Contains(got, `href="settings.cgi"`) {
		t.Fatal("single local", got)
	}
}

func TestMenuLauncherNeedsADSMSession(t *testing.T) {
	cfg, src := menuInputs()
	for _, auth := range []*menuAuth{nil, {err: api.ErrNoSession}, {err: api.ErrAppPrivilegeDenied}} {
		w := openMenu(t, cfg, src, auth, true, "https://dsm.example.com/open.cgi")
		body := w.Body.String()
		if w.Code != 200 || w.Header().Get("Location") != "" || strings.Contains(body, "app.example.com") || strings.Contains(body, "<script") || strings.Contains(body, "launchApp=") {
			t.Fatal(auth, w.Code, w.Header(), body)
		}
	}
	w := openMenu(t, cfg, src, &menuAuth{err: api.ErrNoSession}, true, "https://dsm.example.com/open.cgi")
	if !strings.Contains(w.Body.String(), `data-error-code="sign_in_required"`) {
		t.Fatal(w.Body)
	}
}

func TestMenuLauncherSingleOriginUnreachableHints(t *testing.T) {
	cfg, src := menuInputs()
	cfg.External = &ExternalConfig{SingleOrigin: true}
	src.External = External{Accept: []string{"https://vault.example.ts.net"}, Primary: "https://vault.example.ts.net"}
	admin := &menuAuth{id: api.Identity{User: "admin", Admin: true}}
	w := openMenu(t, cfg, src, admin, true, "https://192.0.2.10:5001/open.cgi")
	body := w.Body.String()
	if !strings.Contains(body, `data-target="https://vault.example.ts.net/"`) || !strings.Contains(body, "only from devices on the tailnet") || !strings.Contains(body, `href="settings.cgi"`) || strings.Contains(body, "launchApp=") {
		t.Fatal(body)
	}
}

func TestMenuCatalogs(t *testing.T) {
	var keys []string
	en, _ := denialCatalogs.ReadFile("i18n/en.json")
	var base map[string]string
	if err := json.Unmarshal(en, &base); err != nil {
		t.Fatal(err)
	}
	for key := range base {
		if strings.HasPrefix(key, "menu_") {
			keys = append(keys, key)
		}
	}
	if len(keys) < 19 {
		t.Fatal("menu keys", keys)
	}
	for _, locale := range denialLocales {
		raw, _ := denialCatalogs.ReadFile("i18n/" + locale + ".json")
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(locale, err)
		}
		for _, key := range keys {
			v := strings.TrimSpace(m[key])
			if v == "" || strings.HasSuffix(v, ".") || strings.HasSuffix(v, "。") {
				t.Errorf("%s %s: %q", locale, key, v)
			}
			if strings.Contains(base[key], "{provider}") != strings.Contains(v, "{provider}") {
				t.Errorf("%s %s: placeholder", locale, key)
			}
		}
	}
}

func TestMenuProbeScriptScenarios(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("browser script tests require Node, provided by the SDK gate")
	}
	script := filepath.Join(t.TempDir(), "probe.js")
	if err := os.WriteFile(script, []byte(menuProbeScript), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--test", "menu_probe.test.mjs")
	cmd.Env = append(os.Environ(), "SPL_MENU_PROBE="+script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("probe behavior: %v\n%s", err, out)
	}
}
