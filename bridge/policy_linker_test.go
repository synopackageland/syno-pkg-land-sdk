//go:build unix

package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Use a compiled CGI with the same API and bridge pins as a packaged App.
// In-process runtime tests without -X cannot detect a synthetic policy being
// checked against the App's real policy pin during manifest validation.
func TestPackagedBridgePolicyBindings(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	const module = "github.com/synopackageland/syno-pkg-land-sdk"
	dir := t.TempDir()
	write := func(name string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", fmt.Appendf(nil, "module bridgeprobe\n\ngo 1.25.0\nrequire %s v0.0.0\nreplace %s => %q\n", module, module, filepath.ToSlash(root)))
	write("main.go", []byte(`package main
import (
 "fmt"; "net/http"; "net/http/cgi"; "os"; "strings"
 "github.com/synopackageland/syno-pkg-land-sdk/api"
 "github.com/synopackageland/syno-pkg-land-sdk/bridge"
)
var manifestDigest, permissionsDigest string
type auth struct{}
func(auth) Authenticate(*http.Request)(api.Identity,error){return api.Identity{},api.ErrNoSession}
func main(){
 // Every language goes through the same manifest method validation.
 for _,runtime:=range []string{"python","node","rust"} {
  extra:=""; if runtime=="node" {extra=",\"node\":{\"package\":\"Node.js_v22\"}"}
  raw:=fmt.Sprintf("{\"schema\":1,\"runtime\":%q,\"entry\":\"app\",\"mode\":\"exec\",\"methods\":[%q]%s}",runtime,os.Args[2],extra)
  if _,err:=bridge.ParseManifest([]byte(raw));err!=nil {fmt.Fprint(os.Stderr,err);os.Exit(1)}
 }
 policyPin:=permissionsDigest; if len(os.Args)>3 {policyPin=""} // Exercise the independent API pin, too.
 h,err:=bridge.NewRuntime(bridge.Config{PackageID:"BridgePolicyProbe",AppDir:os.Args[1],DataDir:os.Args[1],Auth:auth{},ManifestDigest:manifestDigest,PermissionsDigest:policyPin})
 if err!=nil {fmt.Fprint(os.Stderr,err);os.Exit(1)}
 if strings.Contains(os.Args[2],"sdk") {panic("reserved SDK method accepted")}
 if err:=cgi.Serve(h);err!=nil {fmt.Fprint(os.Stderr,err);os.Exit(1)}
}`))
	manifest := []byte(`{"schema":1,"runtime":"python","entry":"never-started.py","mode":"exec","access":"app-privilege","methods":["app.read"]}`)
	const compact = `{"schema":1,"default":"administrators","methods":{"app.read":"authenticated"}}`
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(compact), "", "  "); err != nil {
		t.Fatal(err)
	}
	sdk, err := os.ReadFile(filepath.Join(root, "api", "sdk-permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, policy := range map[string]string{
		"pretty": pretty.String(), "compact": compact, "newline": pretty.String() + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			write(ManifestFile, manifest)
			write(PermissionsFile, []byte(policy))
			binary := filepath.Join(dir, "probe")
			flags := fmt.Sprintf("-X main.manifestDigest=%x -X main.permissionsDigest=%x -X %s/api.expectedPermissionsDigest=%x -X %s/api.expectedSDKPermissionsDigest=%x -X %s/api.expectedEntryMode=app-privilege", sha256.Sum256(manifest), sha256.Sum256([]byte(policy)), module, sha256.Sum256([]byte(policy)), module, sha256.Sum256(sdk), module)
			build := exec.Command("go", "build", "-mod=mod", "-buildvcs=false", "-ldflags", flags, "-o", binary, ".")
			build.Dir = dir
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, out)
			}
			run := func(method string, extra ...string) ([]byte, error) {
				request := `{"method":"app.read","params":{}}`
				cmd := exec.Command(binary, append([]string{dir, method}, extra...)...)
				cmd.Env = append(os.Environ(), "GATEWAY_INTERFACE=CGI/1.1", "REQUEST_METHOD=POST", "SERVER_PROTOCOL=HTTP/1.1", "SERVER_NAME=app.example", "SERVER_PORT=443", "HTTPS=on", "HTTP_HOST=app.example", "SCRIPT_NAME=/api.cgi", "CONTENT_TYPE=application/json", fmt.Sprintf("CONTENT_LENGTH=%d", len(request)), "HTTP_ORIGIN=https://app.example", "HTTP_COOKIE=id=fixture", "HTTP_X_SYNO_TOKEN=fixture")
				cmd.Stdin = strings.NewReader(request)
				return cmd.CombinedOutput()
			}
			out, err := run("app.read")
			if err != nil || !bytes.Contains(out, []byte("Content-Type:")) || !bytes.Contains(out, []byte(`"code":"session_required"`)) {
				t.Fatalf("CGI startup: %v\n%s", err, out)
			}
			if out, err := run("delegate.create"); err == nil || !bytes.Contains(out, []byte("SDK")) {
				t.Fatalf("reserved method accepted: %v\n%s", err, out)
			}
			for edit, changed := range map[string]string{
				"semantic":              strings.Replace(policy, "authenticated", "public", 1),
				"post-build whitespace": policy + " ",
			} {
				write(PermissionsFile, []byte(changed))
				if out, err := run("app.read"); err == nil || !bytes.Contains(out, []byte("does not match the package build")) || bytes.Contains(out, []byte("Content-Type:")) {
					t.Fatalf("%s tamper: %v\n%s", edit, err, out)
				}
				if out, err := run("app.read", "api-pin"); err == nil || !bytes.Contains(out, []byte("App permissions do not match package build policy")) {
					t.Fatalf("%s bypassed API pin: %v\n%s", edit, err, out)
				}
			}
		})
	}
}
