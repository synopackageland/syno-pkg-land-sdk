//go:build unix

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
	"github.com/synopackageland/syno-pkg-land-sdk/idgateway"
)

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
