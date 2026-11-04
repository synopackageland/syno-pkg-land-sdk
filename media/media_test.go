package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/secret"
)

type testAuth struct {
	user string
	err  error
	seen int
}

func (a *testAuth) EntryMode() string { return "app-privilege" }
func (a *testAuth) Authenticate(r *http.Request) (api.Identity, error) {
	a.seen++
	if a.err != nil {
		return api.Identity{}, a.err
	}
	if _, err := api.DSMSession(r); err != nil {
		return api.Identity{}, err
	}
	return api.Identity{User: a.user}, nil
}

type reader struct{ *bytes.Reader }

func (reader) Close() error { return nil }

type fixture struct {
	s                         *Service
	normal, fresh             *testAuth
	server                    http.Handler
	cookies                   []*http.Cookie
	now                       time.Time
	generation, uid, revision string
	owner                     bool
	opens                     int
	dir                       string
	t                         *testing.T
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, normal: &testAuth{user: "person"}, fresh: &testAuth{user: "person"}, now: time.Now(), generation: "install-one", uid: "1201", revision: "rev-one", owner: true, dir: t.TempDir()}
	cfg := Config{DataDir: f.dir, Endpoint: "/cgi/api.cgi", Entry: "SYNO.SDS.Demo.Instance", FreshAuth: f.fresh,
		Generation: func(context.Context) (string, error) { return f.generation, nil },
		LookupUID:  func(context.Context, string) (string, error) { return f.uid, nil },
		Authorize: func(_ context.Context, c api.Caller, asset string) (string, error) {
			if !f.owner || asset != "recording" || c.User() != "person" {
				return "", denied()
			}
			return f.revision, nil
		},
		Open: func(context.Context, api.Caller, string) (Asset, error) {
			f.opens++
			return Asset{Content: reader{bytes.NewReader([]byte("0123456789"))}, ContentType: "audio/wav", ETag: `"asset-v1"`}, nil
		},
	}
	var err error
	f.s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.s.now = func() time.Time { return f.now }
	f.server = f.makeServer(f.s)
	return f
}
func (f *fixture) makeServer(s *Service) http.Handler {
	f.t.Helper()
	p := []byte(`{"schema":1,"default":"administrators","methods":{"media.issue":"authenticated","media.renew":"authenticated","media.revoke":"authenticated","media.content":"authenticated"}}`)
	server, err := api.NewServerWithPermissions(s.Authenticator(f.normal), p, s.Routes()...)
	if err != nil {
		f.t.Fatal(err)
	}
	return server
}
func (f *fixture) rpc(method string, params any, token bool) *httptest.ResponseRecorder {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"method": method, "params": params})
	r := httptest.NewRequest("POST", "https://example.test/cgi/api.cgi", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://example.test")
	r.AddCookie(&http.Cookie{Name: "id", Value: "session-one"})
	if token {
		r.Header.Set("X-SYNO-TOKEN", "private-dsm-token")
	}
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	for _, cookie := range w.Result().Cookies() {
		f.cookies = []*http.Cookie{cookie}
	}
	return w
}
func (f *fixture) issue() Grant {
	f.t.Helper()
	w := f.rpc("media.issue", map[string]string{"asset": "recording"}, true)
	var reply struct {
		Success bool
		Data    Grant
	}
	if json.Unmarshal(w.Body.Bytes(), &reply) != nil || !reply.Success {
		f.t.Fatal(w.Body.String())
	}
	return reply.Data
}
func (f *fixture) request(g Grant, method, cookie string, headers map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, "https://example.test"+g.URL, nil)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "id", Value: cookie})
	}
	for _, c := range f.cookies {
		r.AddCookie(c)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}
func TestIssueAndNativeRange(t *testing.T) {
	f := setup(t)
	g := f.issue()
	if !validID(g.ID) || g.TTLMS != DefaultTTL.Milliseconds() || strings.Contains(g.URL, "private-dsm-token") {
		t.Fatal("invalid URL/TTL")
	}
	c := f.cookies[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/cgi/api.cgi" || strings.Contains(c.Value, "private-dsm-token") {
		t.Fatal("unsealed or unscoped cookie")
	}
	for _, tc := range []struct {
		method, ran, ifrange string
		status               int
		body, contentRange   string
	}{
		{"GET", "", "", 200, "0123456789", ""}, {"GET", "bytes=2-5", "", 206, "2345", "bytes 2-5/10"},
		{"HEAD", "", "", 200, "", ""}, {"HEAD", "bytes=2-5", "", 206, "", "bytes 2-5/10"},
		{"GET", "bytes=2-5", `"asset-v1"`, 206, "2345", "bytes 2-5/10"}, {"GET", "bytes=2-5", `"old"`, 200, "0123456789", ""},
		{"GET", "bytes=20-", "", 416, "invalid range: failed to overlap\n", "bytes */10"},
	} {
		w := f.request(g, tc.method, "session-one", map[string]string{"Range": tc.ran, "If-Range": tc.ifrange})
		if w.Code != tc.status || w.Body.String() != tc.body || w.Header().Get("Content-Range") != tc.contentRange {
			t.Fatalf("%+v: status=%d headers=%v body=%s", tc, w.Code, w.Header(), w.Body)
		}
		if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("missing privacy headers")
		}
	}
	w := f.request(g, "GET", "session-one", map[string]string{"If-None-Match": `"asset-v1"`})
	if w.Code != 304 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body)
	}
	if f.fresh.seen != 9 {
		t.Fatal("fresh authentication not repeated", f.fresh.seen)
	}
	// Only hashes and owner metadata are at rest, including across CGI services.
	raw, err := os.ReadFile(filepath.Join(f.s.dir, "grants.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), g.ID) || strings.Contains(string(raw), "session-one") || strings.Contains(string(raw), "private-dsm-token") {
		t.Fatal("credential persisted")
	}
	s2, err := New(f.s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	s2.now = f.s.now
	f.server = f.makeServer(s2)
	if w := f.request(g, "GET", "session-one", nil); w.Code != 200 {
		t.Fatal("CGI reload failed", w.Code, w.Body)
	}
}
func TestRefuseBindingsAndRevocations(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fixture, *Grant)
		cookie string
	}{
		{"missing-cookie", func(*fixture, *Grant) {}, ""}, {"other-session", func(*fixture, *Grant) {}, "session-two"},
		{"subject", func(f *fixture, _ *Grant) { f.fresh.user = "someone-else" }, "session-one"},
		{"uid", func(f *fixture, _ *Grant) { f.uid = "1202" }, "session-one"},
		{"generation", func(f *fixture, _ *Grant) { f.generation = "install-two" }, "session-one"},
		{"entry", func(f *fixture, _ *Grant) { f.s.cfg.Entry = "SYNO.SDS.Other.Instance" }, "session-one"},
		{"owner", func(f *fixture, _ *Grant) { f.owner = false }, "session-one"},
		{"owner-revision", func(f *fixture, _ *Grant) { f.revision = "rev-two" }, "session-one"},
		{"expired", func(f *fixture, _ *Grant) { f.now = f.now.Add(DefaultTTL) }, "session-one"},
		{"logout", func(f *fixture, _ *Grant) { f.fresh.err = api.ErrNoSession }, "session-one"},
		{"app-revoked", func(f *fixture, _ *Grant) { f.fresh.err = api.ErrAppPrivilegeDenied }, "session-one"},
		{"explicit-revoke", func(f *fixture, g *Grant) {
			if err := f.s.Revoke(g.ID); err != nil {
				t.Fatal(err)
			}
		}, "session-one"},
		{"seal-tampered", func(f *fixture, _ *Grant) { f.cookies[0].Value = "invalid" }, "session-one"},
		{"no-seal", func(f *fixture, _ *Grant) { f.cookies = nil }, "session-one"},
		{"asset-tampered", func(_ *fixture, g *Grant) { g.URL += "&asset=another" }, "session-one"},
		{"duplicate-grant", func(_ *fixture, g *Grant) { g.URL += "&media_grant=" + g.ID }, "session-one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			g := f.issue()
			tc.change(f, &g)
			w := f.request(g, "GET", tc.cookie, nil)
			if w.Code < 400 || f.opens != 0 {
				t.Fatal("accepted invalid binding", w.Code, w.Body)
			}
		})
	}
}
func TestIssueRequiresFullAdmissionAndOwner(t *testing.T) {
	f := setup(t)
	w := f.rpc("media.issue", map[string]string{"asset": "recording"}, false)
	if !strings.Contains(w.Body.String(), "credential_not_ready") && !strings.Contains(w.Body.String(), "auth_unavailable") {
		t.Fatal(w.Body)
	}
	f.owner = false
	w = f.rpc("media.issue", map[string]string{"asset": "recording"}, true)
	if !strings.Contains(w.Body.String(), "permission_denied") {
		t.Fatal(w.Body)
	}
}
func TestRenewSameURLAndRevokeCannotResurrect(t *testing.T) {
	f := setup(t)
	g := f.issue()
	f.now = f.now.Add(DefaultTTL / 2)
	w := f.rpc("media.renew", map[string]string{"media_grant": g.ID}, true)
	var result struct {
		Success bool
		Data    Grant
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if !result.Success || result.Data.URL != g.URL || !result.Data.ExpiresAt.After(g.ExpiresAt) {
		t.Fatal(w.Body)
	}
	f.now = f.now.Add(DefaultTTL / 2)
	if w := f.request(g, "GET", "session-one", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := f.rpc("media.revoke", map[string]string{"media_grant": g.ID}, true); !strings.Contains(w.Body.String(), `"revoked":true`) {
		t.Fatal(w.Body)
	}
	w = f.rpc("media.renew", map[string]string{"media_grant": g.ID}, true)
	if !strings.Contains(w.Body.String(), "permission_denied") {
		t.Fatal("revived", w.Body)
	}
	if w := f.request(g, "GET", "session-one", nil); w.Code < 400 {
		t.Fatal("revived")
	}
}
func TestMediaOnlyAndOrigin(t *testing.T) {
	f := setup(t)
	g := f.issue()
	for _, method := range []string{"POST", "PUT", "DELETE", "OPTIONS"} {
		w := f.request(g, method, "session-one", map[string]string{"Sec-Fetch-Site": "same-origin"})
		if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatal(method, w.Code, w.Body)
		}
	}
	w := f.request(g, "GET", "session-one", map[string]string{"Sec-Fetch-Site": "cross-site"})
	if w.Code != 403 {
		t.Fatal("cross-site", w.Code)
	}
	// A sealed media cookie is never used to authenticate normal RPCs.
	w = f.rpc("media.renew", map[string]string{"media_grant": g.ID}, false)
	if strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatal("media bypass used on RPC")
	}
}
func TestLogsAndResponses(t *testing.T) {
	f := setup(t)
	g := f.issue()
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	f.server = api.WithObserver(f.server, func(r *http.Request, o api.Outcome) {
		fmt.Fprint(&logs, r.RequestURI, r.Header, secret.Redact(map[string]any{"media_grant": g.ID, "grant": "ordinary-value", "url": g.URL, "Set-Cookie": "private-cookie"}))
	})
	if w := f.request(g, "GET", "session-one", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if strings.Contains(logs.String(), "private-cookie") || strings.Contains(logs.String(), g.ID) || strings.Contains(logs.String(), "private-dsm-token") || strings.Contains(logs.String(), f.cookies[0].Value) || !strings.Contains(logs.String(), "ordinary-value") {
		t.Fatal("credential leaked or imprecise redaction")
	}
	// The client still receives the real grant id and URL.
	if !strings.Contains(g.URL, g.ID) {
		t.Fatal("client response was redacted")
	}
}
func TestStoreConcurrencyAndFailClosed(t *testing.T) {
	f := setup(t)
	g := f.issue()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := f.s.Revoke(g.ID); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if w := f.request(g, "GET", "session-one", nil); w.Code < 400 {
		t.Fatal("revoke lost")
	}
	if err := os.WriteFile(filepath.Join(f.s.dir, "grants.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Revoke(g.ID); err == nil {
		t.Fatal("corrupt store accepted")
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("empty config accepted")
	}
	if err := f.s.Revoke("../invalid"); err == nil {
		t.Fatal("invalid id accepted")
	}
}
func TestOpenFailureDoesNotLeakPaths(t *testing.T) {
	f := setup(t)
	g := f.issue()
	f.s.cfg.Open = func(context.Context, api.Caller, string) (Asset, error) {
		return Asset{}, errors.New("sensitive-private-path")
	}
	w := f.request(g, "GET", "session-one", nil)
	if w.Code != 500 || strings.Contains(w.Body.String(), "sensitive-private-path") {
		t.Fatal(w.Code, w.Body)
	}
	_, _ = io.Copy(io.Discard, w.Result().Body)
}

func TestRangeActivityRefreshesSealAndKeepsSecure(t *testing.T) {
	f := setup(t)
	g := f.issue()
	for range 3 {
		f.now = f.now.Add(DefaultTTL * 3 / 4)
		r := httptest.NewRequest("GET", "http://example.test"+g.URL, nil)
		// Models an HTTPS reverse proxy whose backend request has no TLS/Origin.
		r.AddCookie(&http.Cookie{Name: "id", Value: "session-one"})
		for _, c := range f.cookies {
			r.AddCookie(c)
		}
		r.Header.Set("Range", "bytes=1-2")
		w := httptest.NewRecorder()
		f.server.ServeHTTP(w, r)
		if w.Code != 206 {
			t.Fatal("active playback expired", w.Code, w.Body)
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || !cookies[0].Secure {
			t.Fatal("cookie not refreshed securely")
		}
		f.cookies = cookies
	}
	// A valid on-disk record does not make an expired encrypted cookie valid.
	old := f.cookies
	f.now = f.now.Add(DefaultTTL)
	if err := f.s.transaction(func(records map[string]record) error {
		records[digest(g.ID)] = record{Binding: binding{User: "person", UID: f.uid, Entry: f.s.cfg.Entry, Generation: f.generation, Session: digest("session-one")}, Asset: "recording", Revision: f.revision, Methods: []string{"GET", "HEAD"}, Expires: f.now.Add(DefaultTTL)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if w := f.request(g, "GET", "session-one", nil); w.Code < 400 {
		t.Fatal("expired seal accepted")
	}
	f.cookies = old
}

func TestLimitsAndSnapshotRenewal(t *testing.T) {
	f := setup(t)
	g := f.issue()
	var snapshot record
	if err := f.s.transaction(func(records map[string]record) error { snapshot = records[digest(g.ID)]; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Revoke(g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.extend(g.ID, snapshot); err == nil {
		t.Fatal("stale snapshot resurrected a revoked grant")
	}
	if err := f.s.transaction(func(records map[string]record) error {
		for i := range maxGrants {
			records[fmt.Sprint(i)] = snapshot
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w := f.rpc("media.issue", map[string]string{"asset": "recording"}, true)
	if !strings.Contains(w.Body.String(), "capacity reached") {
		t.Fatal("unbounded grants", w.Body)
	}
	cfg := f.s.cfg
	for _, endpoint := range []string{"//other.example/api", "/cgi/../api", "/api?token=bad", "/api%2fother"} {
		cfg.Endpoint = endpoint
		if _, err := New(cfg); err == nil {
			t.Fatal("invalid endpoint accepted", endpoint)
		}
	}
	cfg = f.s.cfg
	cfg.TTL = MaxTTL + 1
	if _, err := New(cfg); err == nil {
		t.Fatal("TTL ceiling not applied")
	}
}
