package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type fixture struct {
	t       *testing.T
	service *Service
	server  *httptest.Server
	now     time.Time
	valid   bool
	code    int
	otp     bool
	logout  int
	calls   []string
	dir     string
	// synoToken makes the fake native authenticator require the session cookie
	// and this token, as DSM web logins do.
	synoToken string
	// token is the X-SYNO-TOKEN the browser sends with each App request.
	token string
}

func setup(t *testing.T) *fixture {
	f := &fixture{t: t, now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), valid: true, dir: t.TempDir()}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		name := r.Form.Get("api") + "." + r.Form.Get("method")
		f.calls = append(f.calls, name)
		switch name {
		case "SYNO.Core.Desktop.Initdata.get":
			fmt.Fprint(w, `{"success":true,"data":{"isLogined":false,"user":"alice"}}`)
		case "SYNO.API.Auth.login":
			if r.Form.Get("session") != "" || r.Form.Get("version") != "7" {
				t.Errorf("wrong auth parameters: %v", r.Form)
			}
			if f.code != 0 {
				fmt.Fprintf(w, `{"success":false,"error":{"code":%d}}`, f.code)
			} else if f.otp && r.Form.Get("otp_code") != "123456" {
				fmt.Fprint(w, `{"success":false,"error":{"code":403,"types":[{"type":"otp"}]}}`)
			} else {
				fmt.Fprint(w, `{"success":true,"data":{"sid":"secret-dsm-sid"}}`)
			}
		case "SYNO.API.Auth.logout":
			f.logout++
			if r.Form.Get("_sid") != "secret-dsm-sid" {
				t.Error("missing logout SID")
			}
			fmt.Fprint(w, `{"success":true}`)
		default:
			t.Errorf("unexpected DSM call %s", name)
		}
	}))
	t.Cleanup(f.server.Close)
	var err error
	f.service, err = New(Config{DataDir: f.dir, CookiePath: "/webman/3rdparty/test/", Endpoint: f.server.URL, Now: func() time.Time { return f.now }, Lookup: func(name string) (Subject, error) {
		if name != "alice" {
			return Subject{}, fmt.Errorf("missing")
		}
		return Subject{User: name, UID: "1205"}, nil
	}, Administrator: func(context.Context, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	f.service.dsmAuth = nativeDSMAuth(func(r *http.Request) (api.Identity, error) {
		f.calls = append(f.calls, "authenticate.cgi")
		c, err := r.Cookie("id")
		if !f.valid || err != nil || c.Value != "valid-dsm-cookie" || (f.synoToken != "" && synoToken(r) != f.synoToken) {
			return api.Identity{}, api.ErrNoSession
		}
		return api.Identity{User: "alice"}, nil
	})
	return f
}
func (f *fixture) call(method, params string, appCookie *http.Cookie, dsmCookie bool) (*httptest.ResponseRecorder, map[string]any) {
	f.t.Helper()
	req := httptest.NewRequest("POST", "https://nas:5001/webman/3rdparty/test/cgi/api.cgi", strings.NewReader(`{"method":"`+method+`","params":`+params+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://nas:5001")
	req.RemoteAddr = "192.0.2.1:9000"
	if appCookie != nil {
		req.AddCookie(appCookie)
	}
	if dsmCookie {
		req.AddCookie(&http.Cookie{Name: "id", Value: "valid-dsm-cookie"})
	}
	if f.token != "" {
		req.Header.Set("X-SYNO-TOKEN", f.token)
	}
	out := httptest.NewRecorder()
	f.service.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := f.service.Authenticate(r)
		if err != nil {
			reply(w, nil, "session_required")
			return
		}
		reply(w, map[string]any{"user": id.User}, "")
	})).ServeHTTP(out, req)
	var body map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
		f.t.Fatalf("bad response: %s", out.Body.String())
	}
	return out, body
}
func errorCode(out map[string]any) string {
	if e, ok := out["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge > 0 {
			return c
		}
	}
	t.Fatalf("no session cookie: %s", rec.Body.String())
	return nil
}
func TestCookieExchangeAndNoInitdataShortcut(t *testing.T) {
	f := setup(t)
	if _, out := f.call("identity.session", `{"bootstrap":true}`, nil, false); errorCode(out) != "session_required" {
		t.Fatalf("anonymous: %v", out)
	}
	f.valid = false
	if _, out := f.call("identity.session", `{"bootstrap":true}`, nil, true); errorCode(out) != "session_required" {
		t.Fatalf("invalid: %v", out)
	}
	f.valid = true
	rec, out := f.call("identity.session", `{"bootstrap":true}`, nil, true)
	if errorCode(out) != "" || out["data"].(map[string]any)["user"] != "alice" {
		t.Fatal(out)
	}
	c := sessionCookie(t, rec)
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/webman/3rdparty/test/" || len(c.Value) < 32 {
		t.Fatalf("cookie: %+v", c)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "Initdata") {
			t.Fatal("Initdata must never authenticate")
		}
	}
}
func TestSynoTokenSessionBootstrapsAndRevalidates(t *testing.T) {
	f := setup(t)
	f.synoToken = "secret-syno-token"
	if _, out := f.call("identity.session", `{"bootstrap":true}`, nil, true); errorCode(out) != "session_required" {
		t.Fatalf("cookie alone must not pass a SynoToken session: %v", out)
	}
	f.token = "secret-syno-token"
	rec, out := f.call("identity.session", `{"bootstrap":true}`, nil, true)
	if errorCode(out) != "" {
		t.Fatalf("bootstrap with SynoToken: %v", out)
	}
	c := sessionCookie(t, rec)
	if _, out = f.call("greeting.set", `{"text":"x"}`, c, true); errorCode(out) != "" {
		t.Fatalf("live revalidation with SynoToken: %v", out)
	}
	filepath.WalkDir(f.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if raw, _ := os.ReadFile(path); !d.IsDir() && strings.Contains(string(raw), "secret-syno-token") {
			t.Fatalf("SynoToken written to %s", path)
		}
		return nil
	})
	f.token = ""
	if _, out = f.call("greeting.set", `{"text":"x"}`, c, true); errorCode(out) != "session_required" {
		t.Fatalf("revalidation without SynoToken: %v", out)
	}
}
func TestTokenlessSessionStillVerifiesWhenBrowserSendsToken(t *testing.T) {
	f := setup(t)
	f.token = "unused-token"
	if _, out := f.call("identity.session", `{"bootstrap":true}`, nil, true); errorCode(out) != "" {
		t.Fatal(out)
	}
}
func TestLoginOTPLogoutAndNoCredentialsOnDisk(t *testing.T) {
	f := setup(t)
	f.otp = true
	if _, out := f.call("identity.login", `{"account":"alice","password":"secret-password"}`, nil, false); errorCode(out) != "otp_required" {
		t.Fatal(out)
	}
	rec, out := f.call("identity.login", `{"account":"alice","password":"secret-password","otp":"123456"}`, nil, false)
	if errorCode(out) != "" || f.logout != 1 {
		t.Fatalf("%v logout=%d", out, f.logout)
	}
	c := sessionCookie(t, rec)
	if _, out = f.call("identity.session", `{}`, c, false); errorCode(out) != "" {
		t.Fatal(out)
	}
	filepath.WalkDir(f.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if !d.IsDir() {
			raw, _ := os.ReadFile(path)
			for _, secret := range []string{"secret-password", "secret-dsm-sid", "valid-dsm-cookie", c.Value} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("secret written to %s", path)
				}
			}
		}
		return nil
	})
	if _, out = f.call("identity.logout", `{}`, c, false); errorCode(out) != "" {
		t.Fatal(out)
	}
	if _, out = f.call("identity.session", `{}`, c, false); errorCode(out) != "session_required" {
		t.Fatal(out)
	}
}
func TestStableLoginErrors(t *testing.T) {
	for _, tc := range []struct {
		code int
		want string
	}{{400, "invalid_credentials"}, {401, "account_disabled"}, {402, "login_not_permitted"}, {403, "login_not_permitted"}} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			f := setup(t)
			f.code = tc.code
			_, out := f.call("identity.login", `{"account":"alice","password":"wrong"}`, nil, false)
			if errorCode(out) != tc.want {
				t.Fatal(out)
			}
			if out["error"].(map[string]any)["message"] != "Sign-in could not be completed." {
				t.Fatal("account detail leaked")
			}
		})
	}
}
func TestExpiryDisableAndSensitiveRevalidation(t *testing.T) {
	f := setup(t)
	rec, _ := f.call("identity.session", `{"bootstrap":true}`, nil, true)
	c := sessionCookie(t, rec)
	f.valid = false
	if _, out := f.call("greeting.set", `{"text":"x"}`, c, true); errorCode(out) != "session_required" {
		t.Fatalf("disabled account accepted: %v", out)
	}
	if _, out := f.call("identity.session", `{}`, c, false); errorCode(out) != "session_required" {
		t.Fatal("revoked session survived")
	}
	rec, _ = f.call("identity.login", `{"account":"alice","password":"p"}`, nil, false)
	c = sessionCookie(t, rec)
	f.now = f.now.Add(15 * time.Minute)
	if _, out := f.call("identity.session", `{}`, c, false); errorCode(out) != "session_required" {
		t.Fatalf("expired: %v", out)
	}
}
func TestSlidingRequiresLiveAccountAndPersistsAcrossCGI(t *testing.T) {
	f := setup(t)
	rec, _ := f.call("identity.session", `{"bootstrap":true}`, nil, true)
	c := sessionCookie(t, rec)
	f.now = f.now.Add(14 * time.Minute)
	rec, out := f.call("identity.session", `{}`, c, true)
	if errorCode(out) != "" {
		t.Fatal(out)
	}
	c = sessionCookie(t, rec)
	// Each CGI creates a new service; the same disk record must remain usable.
	cfg := f.service.cfg
	var err error
	f.service, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(2 * time.Minute)
	if _, out = f.call("identity.session", `{}`, c, false); errorCode(out) != "" {
		t.Fatal(out)
	}
	f.now = f.now.Add(13 * time.Minute)
	if _, out = f.call("identity.session", `{}`, c, false); errorCode(out) != "session_required" {
		t.Fatal("password-only reads slid expiry")
	}
}
func TestRateLimitsPersistByIPAndAccount(t *testing.T) {
	f := setup(t)
	f.code = 400
	for i := 0; i < 5; i++ {
		_, out := f.call("identity.login", `{"account":"alice","password":"wrong"}`, nil, false)
		if errorCode(out) != "invalid_credentials" {
			t.Fatal(out)
		}
	}
	cfg := f.service.cfg
	f.service, _ = New(cfg)
	_, out := f.call("identity.login", `{"account":"alice","password":"wrong"}`, nil, false)
	if errorCode(out) != "rate_limited" {
		t.Fatal(out)
	}
	f.now = f.now.Add(time.Minute)
	_, out = f.call("identity.login", `{"account":"alice","password":"wrong"}`, nil, false)
	if errorCode(out) != "invalid_credentials" {
		t.Fatal(out)
	}
}

// The transport applies api.CheckOrigin (api/REQUESTS.md) before any
// identity or business step, transfers included.
func TestTransportGuards(t *testing.T) {
	f := setup(t)
	login := `{"method":"identity.login","params":{"account":"alice","password":"secret"}}`
	status := `{"method":"identity.status","params":{}}`
	for _, tc := range []struct {
		name, url, body string
		headers         map[string]string
		want            string
	}{
		{"HTTP", "http://nas:5001/", login, map[string]string{"Origin": "http://nas:5001"}, "https_required"},
		{"missing origin", "https://nas:5001/", login, nil, "origin_required"},
		{"cross origin", "https://nas:5001/", login, map[string]string{"Origin": "https://evil.test"}, "cross_origin"},
		{"opaque origin", "https://nas:5001/", login, map[string]string{"Origin": "null"}, "cross_origin"},
		{"cross site", "https://nas:5001/", login, map[string]string{"Origin": "https://nas:5001", "Sec-Fetch-Site": "cross-site"}, "cross_origin"},
		{"another port", "https://nas:5001/", login, map[string]string{"Sec-Fetch-Site": "same-site"}, "cross_origin"},
		{"cross site business", "https://nas:5001/", `{"method":"greeting.set","params":{}}`, map[string]string{"Origin": "https://evil.test"}, "cross_origin"},
		{"cross site transfer", "https://nas:5001/?method=files.upload", "", map[string]string{"Sec-Fetch-Site": "cross-site"}, "cross_origin"},
		{"transfer without origin", "https://nas:5001/?method=files.upload", "", nil, "origin_required"},
		{"proxy rewrote Host", "https://nas:5001/", status, map[string]string{"Origin": "https://apps.example", "Sec-Fetch-Site": "same-origin"}, ""},
		{"forwarded host", "https://nas:5001/", status, map[string]string{"Origin": "https://apps.example", "X-Forwarded-Host": "apps.example"}, ""},
		{"referer", "https://nas:5001/", status, map[string]string{"Referer": "https://nas:5001/app/"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tc.url, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			f.service.Wrap(http.NotFoundHandler()).ServeHTTP(w, r)
			var out map[string]any
			json.Unmarshal(w.Body.Bytes(), &out)
			if errorCode(out) != tc.want || tc.want == "" && out["success"] != true {
				t.Fatal(out)
			}
		})
	}
	if len(f.calls) != 0 {
		t.Fatal("transport refusal reached DSM")
	}
}
func TestUIDChangeAndMissingCookieFailClosed(t *testing.T) {
	f := setup(t)
	rec, _ := f.call("identity.login", `{"account":"alice","password":"p"}`, nil, false)
	c := sessionCookie(t, rec)
	if _, out := f.call("greeting.set", `{}`, c, false); errorCode(out) != "session_revalidation_required" {
		t.Fatal(out)
	}
	f.service.cfg.Lookup = func(name string) (Subject, error) { return Subject{User: name, UID: "9999"}, nil }
	if _, out := f.call("identity.session", `{}`, c, false); errorCode(out) != "session_required" {
		t.Fatal("UID replacement inherited session")
	}
}
func TestStatusAndPermissionsRemainSeparate(t *testing.T) {
	f := setup(t)
	_, out := f.call("identity.status", `{}`, nil, false)
	if out["data"].(map[string]any)["enabled"] != true {
		t.Fatal(out)
	}
	srv, err := api.NewServerWithPermissions(f.service, []byte(`{"schema":1,"default":"administrators","methods":{"greeting.get":"authenticated"}}`), api.Handle("greeting.get", func(context.Context, api.Caller, json.RawMessage) (any, error) { return "ok", nil }))
	if err != nil {
		t.Fatal(err)
	}
	_ = srv
}

func TestRateLimitsAccountAcrossIPsAndIPAcrossAccounts(t *testing.T) {
	for _, mode := range []string{"account", "ip"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			f.code = 400
			limit := 5
			if mode == "ip" {
				limit = 20
			}
			for i := 0; i <= limit; i++ {
				account := "alice"
				ip := fmt.Sprintf("192.0.2.%d:9000", i+1)
				if mode == "ip" {
					account = fmt.Sprintf("user%d", i)
					ip = "192.0.2.1:9000"
				}
				req := httptest.NewRequest("POST", "https://nas:5001/cgi/api.cgi", strings.NewReader(fmt.Sprintf(`{"method":"identity.login","params":{"account":%q,"password":"wrong"}}`, account)))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Origin", "https://nas:5001")
				req.RemoteAddr = ip
				w := httptest.NewRecorder()
				f.service.Wrap(http.NotFoundHandler()).ServeHTTP(w, req)
				var out map[string]any
				json.Unmarshal(w.Body.Bytes(), &out)
				want := "invalid_credentials"
				if i == limit {
					want = "rate_limited"
				}
				if errorCode(out) != want {
					t.Fatalf("attempt %d: %v", i, out)
				}
			}
		})
	}
}
func TestBusinessPolicyAndPasswordOnlyReads(t *testing.T) {
	f := setup(t)
	rec, _ := f.call("identity.login", `{"account":"alice","password":"p"}`, nil, false)
	c := sessionCookie(t, rec)
	for _, m := range []string{"app.status", "greeting.get"} {
		if _, out := f.call(m, `{}`, c, false); errorCode(out) != "" {
			t.Fatal(out)
		}
	}
	handler := func(context.Context, api.Caller, json.RawMessage) (any, error) { return "ok", nil }
	srv, err := api.NewServerWithPermissions(f.service, []byte(`{"schema":1,"default":"administrators","methods":{"greeting.get":"authenticated"}}`), api.Handle("greeting.get", handler), api.Handle("greeting.set", handler))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, want string }{{"greeting.get", ""}, {"greeting.set", "admin_required"}} {
		r := httptest.NewRequest("POST", "https://nas:5001/cgi/api.cgi", strings.NewReader(`{"method":"`+tc.method+`","params":{}}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://nas:5001")
		r.AddCookie(c)
		r.AddCookie(&http.Cookie{Name: "id", Value: "valid-dsm-cookie"})
		w := httptest.NewRecorder()
		f.service.Wrap(srv).ServeHTTP(w, r)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		if errorCode(out) != tc.want {
			t.Fatal(out)
		}
	}
}
func TestIdentityRejectsMalformedRequestsAndRotates(t *testing.T) {
	f := setup(t)
	rec, _ := f.call("identity.login", `{"account":"alice","password":"p"}`, nil, false)
	old := sessionCookie(t, rec)
	rec, _ = f.call("identity.login", `{"account":"alice","password":"p"}`, old, false)
	fresh := sessionCookie(t, rec)
	if fresh.Value == old.Value {
		t.Fatal("session was not rotated")
	}
	if _, out := f.call("identity.session", `{}`, old, false); errorCode(out) != "session_required" {
		t.Fatal(out)
	}
	if _, out := f.call("identity.session", `{"username":"mallory"}`, fresh, false); errorCode(out) != "bad_request" {
		t.Fatal(out)
	}
	if _, out := f.call("identity.logout", `{}`, fresh, false); errorCode(out) != "" {
		t.Fatal(out)
	}
	rec, out := f.call("identity.session", `{"bootstrap":true}`, fresh, true)
	if errorCode(out) != "session_required" {
		t.Fatalf("revoked session silently reissued: %v", out)
	}
	if len(rec.Result().Cookies()) != 1 || rec.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("missing cookie deletion")
	}
}

func TestAppOnlyFilesRefuseWithDSMCodeAndStreamsReachCentralServer(t *testing.T) {
	f := setup(t)
	login, out := f.call("identity.login", `{"account":"alice","password":"p"}`, nil, false)
	if errorCode(out) != "" {
		t.Fatal(out)
	}
	cookie := sessionCookie(t, login)
	for _, method := range []string{"nas.files.list", "nas.session.status"} {
		_, out = f.call(method, `{}`, cookie, false)
		if errorCode(out) != "dsm_session_required" {
			t.Fatalf("%s: %v", method, out)
		}
	}
	called := false
	srv, err := api.NewServerWithPermissions(f.service, []byte(`{"schema":1,"default":"administrators","methods":{}}`), api.HandleStream("nas.files.download", func(_ context.Context, c api.Caller, w http.ResponseWriter, r *http.Request) error {
		called = true
		_, e := w.Write([]byte(c.User()))
		return e
	}))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://nas:5001/webman/3rdparty/test/cgi/api.cgi?method=nas.files.download&path=/home/a", nil)
	r.Header.Set("Origin", "https://nas:5001")
	r.AddCookie(cookie)
	r.AddCookie(&http.Cookie{Name: "id", Value: "valid-dsm-cookie"})
	w := httptest.NewRecorder()
	f.service.Wrap(srv).ServeHTTP(w, r)
	if !called || w.Body.String() != "alice" {
		t.Fatal(w.Body.String())
	}
}

func TestIdentityUploadDoesNotBufferJSONOrReplaceCallerSession(t *testing.T) {
	f := setup(t)
	login, out := f.call("identity.login", `{"account":"alice","password":"p"}`, nil, false)
	if errorCode(out) != "" {
		t.Fatal(out)
	}
	cookie := sessionCookie(t, login)
	srv, err := api.NewServerWithPermissions(f.service, []byte(`{"schema":1,"default":"administrators","methods":{}}`), api.HandleStream("nas.files.upload", func(_ context.Context, c api.Caller, w http.ResponseWriter, r *http.Request) error {
		if c.User() != "alice" {
			t.Fatal("wrong caller")
		}
		dc, err := r.Cookie("id")
		if err != nil || dc.Value != "valid-dsm-cookie" {
			t.Fatal("DSM session replaced")
		}
		n, err := io.Copy(io.Discard, r.Body)
		if n != 2<<20 {
			t.Fatalf("upload body consumed before handler: %d", n)
		}
		_, err = w.Write([]byte("uploaded"))
		return err
	}))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "https://nas:5001/cgi/api.cgi?method=nas.files.upload&parent=/home&name=a", io.LimitReader(zeroReader{}, 2<<20))
	r.ContentLength = 2 << 20
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("Origin", "https://nas:5001")
	r.AddCookie(cookie)
	r.AddCookie(&http.Cookie{Name: "id", Value: "valid-dsm-cookie"})
	w := httptest.NewRecorder()
	f.service.Wrap(srv).ServeHTTP(w, r)
	if w.Body.String() != "uploaded" {
		t.Fatal(w.Body.String())
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type nativeDSMAuth func(*http.Request) (api.Identity, error)

func (f nativeDSMAuth) Authenticate(r *http.Request) (api.Identity, error) { return f(r) }

func TestBootstrapPreservesNativeDSMRequestContext(t *testing.T) {
	f := setup(t)
	// A loopback NormalUser.get would refuse and invalidate this IP-bound
	// session; only native authentication with the original carriers is valid.
	f.valid = false
	called := false
	f.service.dsmAuth = nativeDSMAuth(func(r *http.Request) (api.Identity, error) {
		called = true
		cookie, err := r.Cookie("id")
		if err != nil || cookie.Value != "valid-dsm-cookie" || r.Header.Get("X-SYNO-TOKEN") != "ip-bound-token" || r.RemoteAddr != "192.0.2.1:9000" {
			t.Fatal("native authentication lost the request context")
		}
		return api.Identity{User: "alice"}, nil
	})
	f.token = "ip-bound-token"
	_, body := f.call("identity.session", `{"bootstrap":true}`, nil, true)
	if body["success"] != true || !called {
		t.Fatalf("native DSM bootstrap refused: success=%v native=%v", body["success"], called)
	}
	for _, call := range f.calls {
		if call == "SYNO.Core.NormalUser.get" {
			t.Fatal("browser session replayed over HTTP")
		}
	}
}
