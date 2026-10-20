package bridge

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type userAuth struct{ id api.Identity }

func (a userAuth) Authenticate(*http.Request) (api.Identity, error) {
	if a.id.User == "" {
		return api.Identity{}, api.ErrNoSession
	}
	return a.id, nil
}

func serve(t *testing.T, srv http.Handler, body string, header map[string]string) string {
	t.Helper()
	r := httptest.NewRequest("POST", "https://nas.local:5001/api.cgi", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://nas.local:5001")
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w.Body.String()
}

func TestRoutesHandTheVerifiedCallerToTheApp(t *testing.T) {
	var got map[string]any
	app := fakeApp{methods: []string{"hello.greet"}, handle: func(_ *fakeConn, call map[string]any) map[string]any {
		got = call["ctx"].(map[string]any)
		return map[string]any{"ok": true, "data": "hi"}
	}}
	routes := Routes(&Session{Dial: pipeDialer(app), Methods: []string{"hello.greet"}})
	srv, err := api.NewServerWithPermissions(userAuth{api.Identity{User: "alice"}}, []byte(`{"schema":1,"default":"administrators","methods":{"hello.greet":"authenticated"}}`), routes...)
	if err != nil {
		t.Fatal(err)
	}
	if body := serve(t, srv, `{"method":"hello.greet","params":{}}`, map[string]string{"Accept-Language": "zh-TW,zh;q=0.9", "Cookie": "id=secret"}); body != `{"data":"hi","success":true}`+"\n" {
		t.Fatalf("body %s", body)
	}
	if got["user"] != "alice" || got["admin"] != false || got["locale"] != "zh-TW" || len(got["request_id"].(string)) != 32 || got["deadline_ms"].(float64) <= 0 {
		t.Fatalf("ctx %v", got)
	}
}

func TestRefusedRequestsNeverReachTheApp(t *testing.T) {
	reached := false
	app := fakeApp{methods: []string{"hello.greet"}, handle: func(*fakeConn, map[string]any) map[string]any {
		reached = true
		return map[string]any{"ok": true}
	}}
	routes := Routes(&Session{Dial: pipeDialer(app), Methods: []string{"hello.greet"}})
	srv, _ := api.NewServerWithPermissions(userAuth{}, []byte(`{"schema":1,"default":"administrators","methods":{"hello.greet":"authenticated"}}`), routes...)
	serve(t, srv, `{"method":"hello.greet"}`, nil)
	serve(t, srv, `{"method":"hello.greet"}`, map[string]string{"Origin": "https://evil.example"})
	if reached {
		t.Fatal("a refused request reached the app")
	}
}

func TestLocaleIsOneSafeTag(t *testing.T) {
	for in, want := range map[string]string{"": "", "en-US,en;q=0.5": "en-US", "x\"y": "", "zh-Hant-TW": "zh-Hant-TW", strings.Repeat("a", 40): ""} {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("Accept-Language", in)
		if got := locale(r); got != want {
			t.Fatalf("%q: %q", in, got)
		}
	}
}

func TestTheBrowserSeesAppUnavailable(t *testing.T) {
	dial := DialerFunc(func(context.Context) (net.Conn, error) { return nil, errors.New("gone") })
	srv, err := api.NewServerWithPermissions(userAuth{api.Identity{User: "alice"}}, []byte(`{"schema":1,"default":"administrators","methods":{"hello.greet":"authenticated"}}`), Routes(&Session{Dial: dial, Methods: []string{"hello.greet"}})...)
	if err != nil {
		t.Fatal(err)
	}
	if body := serve(t, srv, `{"method":"hello.greet"}`, nil); body != `{"error":{"code":"app_unavailable","message":"The App is not available right now. Try again later."},"success":false}`+"\n" {
		t.Fatalf("body %s", body)
	}
}
