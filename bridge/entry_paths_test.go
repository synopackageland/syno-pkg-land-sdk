//go:build unix

package bridge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type refusedEntryAuth struct{}

func (refusedEntryAuth) Authenticate(*http.Request) (api.Identity, error) {
	return api.Identity{}, api.ErrNoSession
}

func TestAdminEntryRejectsEveryBridgeRuntimeBeforeStartingTheApp(t *testing.T) {
	for _, runtime := range []string{"python", "node", "rust"} {
		for _, anonymous := range []bool{true, false} {
			for _, transfer := range []string{"upload", "download"} {
				t.Run(runtime+"/"+transfer+map[bool]string{true: "/anonymous", false: "/ordinary"}[anonymous], func(t *testing.T) {
					extra := ""
					if runtime == "node" {
						extra = `,"node":{"package":"Node.js_v22"}`
					}
					manifest := `{"schema":1,"runtime":"` + runtime + `","entry":"missing-app","mode":"exec","access":"admin-only","methods":["app.public","app.transfer"],"transfers":{"app.transfer":"` + transfer + `"}` + extra + `}`
					policy := `{"schema":1,"default":"administrators","methods":{"app.public":"public","app.transfer":"public"}}`
					var auth api.Authenticator = userAuth{api.Identity{User: "ordinary"}}
					code := "admin_required"
					if anonymous {
						auth, code = refusedEntryAuth{}, "session_required"
					}
					srv := runtimeFor(t, Config{AppDir: appDir(t, manifest, policy, nil), Auth: auth})
					for _, body := range []string{`{"method":"app.public"}`, `{"v":1,"calls":[{"method":"app.public"},{"method":"app.public"}]}`} {
						got := serve(t, srv, body, nil)
						if !strings.Contains(got, `"code":"`+code+`"`) {
							t.Fatalf("App dispatch attempted: %s", got)
						}
					}
					for _, verb := range []string{"GET", "POST"} {
						r := httptest.NewRequest(verb, "https://app.example/api.cgi?method=app.transfer", strings.NewReader("bytes"))
						r.Header.Set("Origin", "https://app.example")
						w := httptest.NewRecorder()
						srv.ServeHTTP(w, r)
						if !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
							t.Fatalf("App transfer attempted: %s", w.Body.String())
						}
					}
				})
			}
		}
	}
}
