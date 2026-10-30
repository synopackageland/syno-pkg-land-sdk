//go:build unix

package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
	"github.com/synopackageland/syno-pkg-land-sdk/idgateway"
)

func TestSettingsCGIRoleBootstrapsWithoutCreatingGatewayKey(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	path := filepath.Join(dir, "gateway.json")
	os.WriteFile(path, []byte(`{"package":"Fixture","mode":"none","upstream":"http://127.0.0.1:8000","listen":{"https":18443},"authorize_cgi":"/webman/3rdparty/Fixture/sso/authorize.cgi","state_dir":"`+state+`"}`), 0600)
	t.Setenv("SCRIPT_NAME", "/webman/3rdparty/Fixture/sso/settings.cgi")
	h, err := authorizeHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.(*idgateway.SettingsCGI); !ok {
		t.Fatalf("wrong CGI role: %T", h)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example:5001/settings.cgi", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "enable_syno_token=yes") {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("GET created private state")
	}
}

func TestProviderOnlyIssuerAlwaysHasDSMAppPrivilegeGate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.json")
	os.WriteFile(path, []byte(`{"package":"Reader","mode":"oidc","upstream_access":"public","access":"dsm-users","upstream":"http://127.0.0.1:8000","listen":{"https":18443},"authorize_cgi":"/webman/3rdparty/Reader/sso/authorize.cgi","state_dir":"`+filepath.Join(dir, "state")+`","oidc":{"issuer_port":18444,"clients":[{"id":"reader","redirect_paths":["/cb"]}]}}`), 0600)
	h, err := authorizeHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	verifier := h.(*idgateway.AuthorizeCGI).Verifier.(idgateway.AccessVerifier)
	if _, ok := verifier.Next.(*dsm.EntryGate); !ok {
		t.Fatal("provider-only OIDC omitted DSM Application Privileges")
	}
}

func TestOIDCNativeLoginDoesNotRequireBrowserInjection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.json")
	os.WriteFile(path, []byte(`{"package":"Reader","mode":"oidc","upstream":"http://127.0.0.1:8000","listen":{"https":18443},"authorize_cgi":"/webman/3rdparty/Reader/sso/authorize.cgi","state_dir":"`+filepath.Join(dir, "state")+`","oidc":{"issuer_port":18444,"native_login":true,"clients":[{"id":"reader","redirect_paths":["/i/oidc/"]}]}}`), 0600)
	h, err := authorizeHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	if h.(*idgateway.AuthorizeCGI).LoginApp != "Reader" {
		t.Fatal("native DSM login still depends on browser injection")
	}
}

func TestUsageAndConfigErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"serve"}, {"run", "--config"}, {"run", "--config", filepath.Join(t.TempDir(), "missing.json")}, {"exec"}, {"exec", "--"}, {"exec", "x", "--", "/bin/true"}} {
		if err := run(args); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestCGIRoleServesTheAuthorizePage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.json")
	os.WriteFile(path, []byte(`{"package":"paperless","mode":"header","upstream":"http://127.0.0.1:8000","listen":{"https":18443},
"authorize_cgi":"/webman/3rdparty/paperless/sso/authorize.cgi","state_dir":"`+filepath.Join(dir, "state")+`","headers":{"user":"Remote-User"}}`), 0o644)
	h, err := authorizeHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	h.(*idgateway.AuthorizeCGI).Available = func() bool { return true }
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example:5001/webman/3rdparty/paperless/sso/authorize.cgi?txn=abc&return=https%3A%2F%2Fnas.example%3A18443%2F_sso%2Fdsm-return", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `method="post"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestExecReplacesItselfWithTheApp(t *testing.T) {
	if os.Getenv("GATEWAY_EXEC_CHILD") == "1" {
		run([]string{"exec", "--", "/bin/sh", "-c", "exit 7"})
		os.Exit(1)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecReplacesItselfWithTheApp$")
	cmd.Env = append(os.Environ(), "GATEWAY_EXEC_CHILD=1")
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("%v", err)
	}
}

func TestAccountCommandsNeedAnAccountSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.json")
	os.WriteFile(path, []byte(`{"package":"app","mode":"header","upstream":"http://127.0.0.1:8000","listen":{"https":18443},
"authorize_cgi":"/webman/3rdparty/app/sso/authorize.cgi","state_dir":"`+filepath.Join(dir, "state")+`","headers":{"user":"Remote-User"}}`), 0o644)
	for _, args := range [][]string{{"account", "status", "--config", path}, {"account", "purge", "1027", "--config", path}} {
		if err := run(args); err == nil || !strings.Contains(err.Error(), "account") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{{"account"}, {"account", "purge"}, {"account", "purge", "x", "--config", path}, {"account", "list"}} {
		if err := run(args); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestAccountStatusPrintsJSON(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	path := filepath.Join(dir, "gateway.json")
	os.WriteFile(path, []byte(`{"package":"app","mode":"header","upstream":"http://127.0.0.1:8000","listen":{"https":18443},
"authorize_cgi":"/webman/3rdparty/app/sso/authorize.cgi","state_dir":"`+state+`","headers":{"user":"Remote-User"},
"account":{"var_dir":"`+dir+`","on_delete":[{"remove":"users/{uid}"}]}}`), 0o644)
	var out strings.Builder
	if err := account([]string{"status", "--json", "--config", path}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != `{"pending":[],"missing":[]}`+"\n" {
		t.Fatalf("%q", out.String())
	}
	out.Reset()
	if err := account([]string{"status", "--config", path}, &out); err != nil || !strings.Contains(out.String(), "no pending deletions") {
		t.Fatalf("%v %q", err, out.String())
	}
}

// Exercise CGI parsing and dispatch in an actual linked binary. No DSM session
// or network service is needed for entry, bootstrap and stale-token fallback.
func TestCompiledCGIExternalResume(t *testing.T) {
	packageName := fmt.Sprintf("ExampleApp%d", os.Getpid())
	listener, err := net.Listen("unix", "/tmp/synopkgland-gateway-"+packageName+".sock")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "gateway.json")
	body := fmt.Sprintf(`{"package":"ExampleApp","native_ingress":true,"mode":"oidc","upstream":"http://127.0.0.1:8000","listen":{"https":18443},"authorize_cgi":"/webman/3rdparty/ExampleApp/sso/authorize.cgi","state_dir":%q,"oidc":{"issuer_port":18444,"native_login":true,"clients":[{"id":"app","redirect_paths":["/cb"]}]}}`, state)
	body = strings.ReplaceAll(body, "ExampleApp", packageName)
	if err := os.WriteFile(config, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	save := func(revision string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(state, "external.json"), []byte(`{"schema":1,"revision":`+revision+`,"manual_origin":"https://app.example.com","dsm_origin":"https://dsm.example.com"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	save("9007199254740993")
	binary := filepath.Join(dir, "gateway")
	build := exec.Command("go", "build", "-ldflags", "-X main.configPath="+config, "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	invoke := func(script, query string) string {
		t.Helper()
		cmd := exec.Command(binary)
		cmd.Env = append(os.Environ(), "GATEWAY_INTERFACE=CGI/1.1", "REQUEST_METHOD=GET", "SERVER_PROTOCOL=HTTP/1.1", "HTTP_HOST=dsm.example.com:5001", "SERVER_NAME=dsm.example.com", "SERVER_PORT=5001", "REMOTE_ADDR=192.0.2.7", "HTTPS=on", "SCRIPT_NAME=/webman/3rdparty/"+packageName+"/sso/"+script, "QUERY_STRING="+query)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CGI: %v %s", err, out)
		}
		return string(out)
	}
	token := "opaque_txn:x0:9007199254740993.0"
	output := invoke("open.cgi", "ssoResume="+url.QueryEscape(token))
	if !strings.Contains(output, "Status: 302") || !strings.Contains(output, "resume="+url.QueryEscape(token)) || !strings.Contains(output, "return=https%3A%2F%2Fapp.example.com%2F_sso%2Fdsm-return") {
		t.Fatal(output)
	}
	output = invoke("authorize.cgi", "resume="+url.QueryEscape(token))
	if !strings.Contains(output, "method=\"post\"") || !strings.Contains(output, "resume="+url.QueryEscape(token)) || !strings.Contains(output, "txn=opaque_txn") {
		t.Fatal(output)
	}
	save("9007199254740994")
	output = invoke("authorize.cgi", "resume="+url.QueryEscape(token))
	if !strings.Contains(output, "Location: https://dsm.example.com:18443/") || strings.Contains(output, "<form") {
		t.Fatal("stale token", output)
	}
}

func TestEveryGatewayModeLaunchesDSMForColdSignIn(t *testing.T) {
	for _, mode := range []string{"none", "header"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "gateway.json")
		os.WriteFile(path, []byte(`{"package":"Reader","mode":"`+mode+`","upstream":"http://127.0.0.1:8000","listen":{"https":18443},"authorize_cgi":"/webman/3rdparty/Reader/sso/authorize.cgi","state_dir":"`+filepath.Join(dir, "state")+`","headers":{"user":"Remote-User"}}`), 0600)
		h, err := authorizeHandler(path)
		if err != nil {
			t.Fatal(err)
		}
		if h.(*idgateway.AuthorizeCGI).LoginApp != "Reader" {
			t.Fatalf("%s omits automatic DSM login", mode)
		}
	}
}
