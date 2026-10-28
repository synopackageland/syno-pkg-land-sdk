package api

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackagedEntryAndPolicyBindingsInCompiledBinary(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(cwd)
	module := "github.com/synopackageland/syno-pkg-land-sdk"
	mod := fmt.Sprintf("module entryprobe\n\ngo 1.25.0\nrequire %s v0.0.0\nreplace %s => %q\n", module, module, filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	const raw = `{"schema":1,"default":"administrators","methods":{"app.public":"public"}}`
	source := `package main
import (
 "context"; "encoding/json"; "fmt"; "net/http"; "net/http/httptest"; "os"; "strings"
 "github.com/synopackageland/syno-pkg-land-sdk/api"
)
type auth struct{}
func(auth) Authenticate(*http.Request)(api.Identity,error){return api.Identity{User:"ordinary"},nil}
func main(){
 raw:=[]byte(` + "`" + raw + "`" + `)
 routes:=[]api.Route{api.Handle("app.public",func(context.Context,api.Caller,json.RawMessage)(any,error){panic("denied handler ran")})}
 if len(os.Args)>1 {switch os.Args[1] {
 case "changed":raw=append(raw,' ')
 case "unlisted":routes=append(routes,api.Handle("app.unlisted",func(context.Context,api.Caller,json.RawMessage)(any,error){panic("unlisted handler ran")}))
 }}
 server,err:=api.NewServerWithPermissions(auth{},raw,routes...)
 if err!=nil {fmt.Print(err);return}
 request:=httptest.NewRequest("POST","https://app.example/api.cgi",strings.NewReader(` + "`" + `{"method":"app.public"}` + "`" + `))
 request.Header.Set("Content-Type","application/json")
 request.Header.Set("Origin","https://app.example")
 response:=httptest.NewRecorder();server.ServeHTTP(response,request);fmt.Print(response.Body.String())
}`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "probe")
	flags := fmt.Sprintf("-X %s/api.expectedEntryMode=admin-only -X %s/api.expectedPermissionsDigest=%x", module, module, sha256.Sum256([]byte(raw)))
	build := exec.Command("go", "build", "-mod=mod", "-buildvcs=false", "-ldflags", flags, "-o", binary, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for arg, want := range map[string]string{
		"":         `"code":"admin_required"`,
		"changed":  "App permissions do not match package build policy",
		"unlisted": "registered API method missing from permissions: app.unlisted",
	} {
		out, err := exec.Command(binary, arg).CombinedOutput()
		if err != nil || !strings.Contains(string(out), want) {
			t.Fatalf("%s: %v %s; want %s", arg, err, out, want)
		}
	}
}
