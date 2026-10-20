package idgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const paperlessConfig = `{
  "package": "paperless",
  "mode": "oidc",
  "upstream": "http://127.0.0.1:8000",
  "listen": {"http": 18080, "https": 18443},
  "authorize_cgi": "/webman/3rdparty/paperless/sso/authorize.cgi",
  "state_dir": "STATE",
  "passthrough": ["/share/"],
  "oidc": {
    "issuer_port": 18090,
    "clients": [{"id": "paperless", "redirect_paths": ["/accounts/oidc/dsm/login/callback/"], "require_pkce": true}],
    "rewrite_json": [{"method": "POST", "path": "/api/auth/login", "field": "location"}]
  }
}`

func writeConfig(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	path := filepath.Join(dir, "gateway.json")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(body, "STATE", state)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, state
}

func TestLoadConfigAppliesDefaults(t *testing.T) {
	path, state := writeConfig(t, paperlessConfig)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Package != "paperless" || cfg.Mode != ModeOIDC || cfg.StateDir != state || cfg.Listen.HTTPS != 18443 {
		t.Fatalf("%+v", cfg)
	}
	if cfg.Issuer() != "http://127.0.0.1:18090" || cfg.SessionTTL() != DefaultSessionTTL {
		t.Fatalf("issuer %s ttl %s", cfg.Issuer(), cfg.SessionTTL())
	}
}

func TestLoadConfigRejectsInvalidConfigs(t *testing.T) {
	for name, edit := range map[string][2]string{
		"unknown field":    {`"mode": "oidc"`, `"mode": "oidc", "debug": true`},
		"relative state":   {`"state_dir": "STATE"`, `"state_dir": "state"`},
		"bad package":      {`"package": "paperless"`, `"package": "../x"`},
		"no listener":      {`{"http": 18080, "https": 18443}`, `{}`},
		"port range":       {`"https": 18443`, `"https": 70000`},
		"oidc without":     {`"oidc": {`, `"x_oidc": {`},
		"no clients":       {`"clients": [{"id": "paperless", "redirect_paths": ["/accounts/oidc/dsm/login/callback/"], "require_pkce": true}]`, `"clients": []`},
		"bad client id":    {`"id": "paperless"`, `"id": "a/b"`},
		"token no command": {`"mode": "oidc"`, `"mode": "token"`},
		"relative var dir": {`"passthrough": ["/share/"],`, `"passthrough": ["/share/"], "account": {"var_dir": "var", "on_delete": []},`},
		"bad remove":       {`"passthrough": ["/share/"],`, `"passthrough": ["/share/"], "account": {"var_dir": "/v", "on_delete": [{"remove": "../{uid}"}]},`},
		"bad placeholder":  {`"passthrough": ["/share/"],`, `"passthrough": ["/share/"], "account": {"var_dir": "/v", "on_delete": [{"exec": ["/bin/app", "{email}"]}]},`},
		"two kinds":        {`"passthrough": ["/share/"],`, `"passthrough": ["/share/"], "account": {"var_dir": "/v", "on_delete": [{"remove": "u/{uid}", "exec": ["/bin/app"]}]},`},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(paperlessConfig, edit[0]) {
				t.Fatalf("fixture lacks %q", edit[0])
			}
			body := strings.Replace(paperlessConfig, edit[0], edit[1], 1)
			if name == "oidc without" {
				body = strings.Replace(body, `"x_oidc": {`, `"passthrough2": {`, 1)
			}
			path, _ := writeConfig(t, body)
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestSecretsAreCreatedPrivateAndStable(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	a, err := GatewayKey(state)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GatewayKey(state)
	if len(a) < MinKeySize || string(a) != string(b) {
		t.Fatalf("key %d stable %v", len(a), string(a) == string(b))
	}
	s1, err := ClientSecret(state, "paperless")
	if err != nil || len(s1) < 40 {
		t.Fatalf("secret %q %v", s1, err)
	}
	s2, _ := ClientSecret(state, "paperless")
	other, _ := ClientSecret(state, "openlist")
	if s1 != s2 || s1 == other {
		t.Fatal("client secrets must be stable per client")
	}
	for _, f := range []string{"gateway.key", "client-paperless.secret"} {
		info, err := os.Stat(filepath.Join(state, f))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, info.Mode(), err)
		}
	}
	if info, _ := os.Stat(state); info.Mode().Perm() != 0o700 {
		t.Fatalf("state dir %v", info.Mode())
	}
	if _, err := ClientSecret(state, "../x"); err == nil {
		t.Fatal("client id escaped the state dir")
	}
}

func TestBuildWiresProviderAndFront(t *testing.T) {
	path, _ := writeConfig(t, paperlessConfig)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	up := newUpstream(t, nil)
	cfg.Upstream = up.URL
	gw, err := Build(cfg, DSMPorts{HTTP: 5000, HTTPS: 5001})
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := ClientSecret(cfg.StateDir, "paperless")
	if gw.Secrets["paperless"] != secret {
		t.Fatal("app secret not exposed to the app wrapper")
	}
	w := httptest.NewRecorder()
	gw.Provider.ServeHTTP(w, httptest.NewRequest("GET", cfg.Issuer()+"/.well-known/openid-configuration", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), cfg.Issuer()) {
		t.Fatalf("discovery %d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "https://nas.example:18443/_sso/authorize?"+url.Values{
		"client_id": {"paperless"}, "redirect_uri": {"https://nas.example:18443/accounts/oidc/dsm/login/callback/"},
		"response_type": {"code"}, "scope": {"openid"}, "code_challenge": {"x"}, "code_challenge_method": {"S256"},
	}.Encode(), nil)
	w = httptest.NewRecorder()
	gw.Front.ServeHTTP(w, r)
	if loc := w.Header().Get("Location"); w.Code != http.StatusFound || !strings.HasPrefix(loc, "https://nas.example:5001/webman/3rdparty/paperless/sso/authorize.cgi?") {
		t.Fatalf("authorize %d %s", w.Code, loc)
	}
}

func TestExecTokensRunsTheAdapter(t *testing.T) {
	src := ExecTokens{Command: []string{"/bin/sh", "-c", `printf '{"headers":{"Authorization":["Bearer %s-%s-%s"]}}' "$SYNOPKGLAND_SSO_UID" "$SYNOPKGLAND_SSO_USER" "$SYNOPKGLAND_SSO_ADMIN"`}}
	h, err := src.Credentials(context.Background(), alice)
	if err != nil || h.Get("Authorization") != "Bearer 1026-alice-1" {
		t.Fatalf("%v %v", h, err)
	}
	for name, cmd := range map[string]string{"fails": "exit 3", "not json": "echo nope", "no headers": "echo '{}'"} {
		if _, err := (ExecTokens{Command: []string{"/bin/sh", "-c", cmd}}).Credentials(context.Background(), alice); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestExecTokensBoundsAdapterOutput(t *testing.T) {
	src := ExecTokens{Command: []string{"/bin/sh", "-c", "yes x | head -c 200000"}}
	if _, err := src.Credentials(context.Background(), alice); err == nil {
		t.Fatal("unbounded output accepted")
	}
}

const accountConfig = `"account": {"var_dir": "VAR", "on_delete": [{"remove": "users/{uid}"}, {"exec": ["/bin/app", "user", "delete", "{name}"]}]},`

func TestBuildRecordsSignInsAndRevokesDeletedAccounts(t *testing.T) {
	varDir := t.TempDir()
	path, state := writeConfig(t, strings.Replace(paperlessConfig, `"passthrough"`, strings.Replace(accountConfig, "VAR", varDir, 1)+` "passthrough"`, 1))
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []DeleteAction{{Remove: "users/{uid}"}, {Exec: []string{"/bin/app", "user", "delete", "{name}"}}}
	if cfg.Account == nil || cfg.Account.VarDir != varDir || !reflect.DeepEqual(cfg.Account.OnDelete, want) {
		t.Fatalf("%+v", cfg.Account)
	}
	gw, err := Build(cfg, DSMPorts{HTTP: 5000, HTTPS: 5001})
	if err != nil {
		t.Fatal(err)
	}
	d := gw.Deletions
	if d == nil || d.StateDir != state || d.VarDir != varDir || d.Passwd != "/etc/passwd" || d.Revoke == nil || d.Deleted == nil || gw.Front.cfg.SignedIn == nil {
		t.Fatalf("%+v", d)
	}
	gw.Front.cfg.SignedIn(bob)
	if seen, _ := d.seen().load(); seen[bob.UID].Name != "bob" {
		t.Fatalf("sign-in not recorded: %v", seen)
	}
	path, _ = writeConfig(t, paperlessConfig)
	cfg, _ = LoadConfig(path)
	if gw, _ := Build(cfg, DSMPorts{}); gw.Deletions != nil || gw.Front.cfg.SignedIn != nil {
		t.Fatal("no account section, yet deletions handled")
	}
}
