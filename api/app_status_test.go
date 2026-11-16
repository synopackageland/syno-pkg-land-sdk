package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func statusRequest(acceptLanguage string) *http.Request {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Accept-Language", acceptLanguage)
	r.Header.Set("Cookie", "id=session")
	r.Header.Set("X-SYNO-TOKEN", "token")
	return r
}

func callStatus(t *testing.T, handler RequestHandler, caller Caller, r *http.Request, params string) AppStatus {
	t.Helper()
	value, err := handler(context.Background(), caller, r, json.RawMessage(params))
	if err != nil {
		t.Fatal(err)
	}
	return value.(AppStatus)
}

func TestAppStatusLanguageAndIdentity(t *testing.T) {
	caller := Caller{identity: Identity{User: "alice", Admin: false}, authenticated: true}
	for _, tc := range []struct{ preference, header, lang, locale string }{
		{"cht", "en-US", "cht", "zh-TW"},
		{"", "ja-JP, en;q=0.8", "", "ja-JP"},
		{"", "bad<script>", "", ""},
		{"", "", "", ""},
	} {
		handler := AppStatusHandler("ExampleApp", "1", func(_ context.Context, _ Caller, r *http.Request) (string, error) {
			if r == nil {
				t.Fatal("custom language without the request")
			}
			return tc.preference, nil
		})
		status := callStatus(t, handler, caller, statusRequest(tc.header), `{}`)
		if status.User != "alice" || status.Admin || status.Lang != tc.lang || status.Locale != tc.locale {
			t.Fatalf("unexpected status: %+v", status)
		}
		raw, _ := json.Marshal(status)
		var wire map[string]any
		json.Unmarshal(raw, &wire)
		if _, ok := wire["lang"]; !ok {
			t.Fatal("missing language field")
		}
		if _, ok := wire["locale"]; !ok {
			t.Fatal("missing locale field")
		}
	}
	_, err := AppStatusHandler("ExampleApp", "1", nil)(context.Background(), Caller{}, nil, nil)
	if err == nil {
		t.Fatal("unauthenticated status succeeded")
	}
}

// A custom language that fails degrades to the browser, never fails app.status.
func TestAppStatusCustomLanguageFailureUsesBrowser(t *testing.T) {
	caller := Caller{identity: Identity{User: "alice"}, authenticated: true}
	handler := AppStatusHandler("ExampleApp", "1", func(context.Context, Caller, *http.Request) (string, error) {
		return "", errors.New("unavailable")
	})
	if status := callStatus(t, handler, caller, statusRequest("ja-JP"), `{}`); status.Locale != "ja-JP" {
		t.Fatalf("status = %+v", status)
	}
}

func withLanguages(t *testing.T, longRunning bool, lookup func(context.Context, map[string]string) (string, bool)) *languageCache {
	t.Helper()
	cache := newLanguageCache()
	cache.lookup = lookup
	cache.longRunning = func() bool { return longRunning }
	saved := defaultLanguages
	defaultLanguages = cache
	t.Cleanup(func() { defaultLanguages = saved })
	return cache
}

func waitIdle(t *testing.T, cache *languageCache, user string) {
	t.Helper()
	for range 500 {
		cache.mu.Lock()
		known := cache.users[user]
		idle := known != nil && !known.reading
		cache.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("background read did not finish")
}

// A Chinese DSM in an English browser: the entry page's hint wins and is
// remembered for the user's later requests without asking DSM.
func TestAppStatusDefaultUsesEntryHint(t *testing.T) {
	cache := withLanguages(t, true, func(context.Context, map[string]string) (string, bool) {
		t.Fatal("a hint must not read DSM")
		return "", false
	})
	alice := Caller{identity: Identity{User: "alice"}, authenticated: true}
	handler := AppStatusHandler("ExampleApp", "1", nil)
	for _, tc := range []struct{ params, lang, locale string }{
		{`{"dsmLanguage":"cht"}`, "cht", "zh-TW"},
		{`{}`, "cht", "zh-TW"},
		{`{"dsmLanguage":"def"}`, "", "en-US"},
		{`{"dsmLanguage":"jpn"}`, "jpn", "ja"},
		{`{"dsmLanguage":""}`, "", "en-US"},
		{`{"dsmLanguage":"rus"}`, "", "en-US"},
		{`{"dsmLanguage":"<script>"}`, "", "en-US"},
	} {
		status := callStatus(t, handler, alice, statusRequest("en-US"), tc.params)
		if status.Lang != tc.lang || status.Locale != tc.locale {
			t.Fatalf("%s: status = %+v", tc.params, status)
		}
	}
	// Another user's hint never changes alice's language.
	bob := Caller{identity: Identity{User: "bob"}, authenticated: true}
	callStatus(t, handler, bob, statusRequest("en-US"), `{"dsmLanguage":"ger"}`)
	if status := callStatus(t, handler, alice, statusRequest("en-US"), `{}`); status.Locale != "en-US" {
		t.Fatalf("alice = %+v", status)
	}
	if cache.users["bob"].lang != "ger" {
		t.Fatalf("bob = %+v", cache.users["bob"])
	}
}

// Without a hint the first reply uses the browser at once; the DSM read runs
// in the background and later replies use it.
func TestAppStatusDefaultReadsDSMInBackground(t *testing.T) {
	release := make(chan struct{})
	var reads atomic.Int32
	cache := withLanguages(t, true, func(_ context.Context, auth map[string]string) (string, bool) {
		reads.Add(1)
		if auth["dsm_cookie"] != "session" || auth["dsm_synotoken"] != "token" {
			t.Errorf("auth = %v", auth)
		}
		<-release
		return "cht", true
	})
	alice := Caller{identity: Identity{User: "alice"}, authenticated: true}
	handler := AppStatusHandler("ExampleApp", "1", nil)
	for range 3 {
		if status := callStatus(t, handler, alice, statusRequest("en-US"), `{}`); status.Locale != "en-US" {
			t.Fatalf("pending read answered %+v", status)
		}
	}
	close(release)
	waitIdle(t, cache, "alice")
	if reads.Load() != 1 {
		t.Fatalf("reads = %d, want one per user at a time", reads.Load())
	}
	if status := callStatus(t, handler, alice, statusRequest("en-US"), `{}`); status.Locale != "zh-TW" {
		t.Fatalf("status = %+v", status)
	}

	// An old value is still answered while it is read again.
	now := time.Now().Add(appLanguageRefresh + time.Second)
	cache.now = func() time.Time { return now }
	cache.lookup = func(context.Context, map[string]string) (string, bool) { return "", false }
	if status := callStatus(t, handler, alice, statusRequest("en-US"), `{}`); status.Locale != "zh-TW" {
		t.Fatalf("stale status = %+v", status)
	}
	waitIdle(t, cache, "alice")
	// A failed read keeps the known value.
	if status := callStatus(t, handler, alice, statusRequest("en-US"), `{}`); status.Locale != "zh-TW" {
		t.Fatalf("after failure = %+v", status)
	}
}

// A CGI process ends with its reply, so it never starts a DSM read; a request
// without DSM session carriers cannot start one either.
func TestAppStatusDefaultWithoutBackgroundRead(t *testing.T) {
	alice := Caller{identity: Identity{User: "alice"}, authenticated: true}
	handler := AppStatusHandler("ExampleApp", "1", nil)
	withLanguages(t, false, func(context.Context, map[string]string) (string, bool) {
		t.Fatal("CGI read DSM")
		return "", false
	})
	if status := callStatus(t, handler, alice, statusRequest("ko-KR"), `{}`); status.Locale != "ko-KR" {
		t.Fatalf("status = %+v", status)
	}
	withLanguages(t, true, func(context.Context, map[string]string) (string, bool) {
		t.Fatal("read DSM without a session")
		return "", false
	})
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Accept-Language", "ko-KR")
	if status := callStatus(t, handler, alice, r, `{}`); status.Locale != "ko-KR" {
		t.Fatalf("status = %+v", status)
	}
}

func TestAppLanguageCacheIsBounded(t *testing.T) {
	cache := newLanguageCache()
	base := time.Now()
	for i := range appLanguageUsers + 5 {
		cache.store(strings.Repeat("u", i+1), "cht", base.Add(time.Duration(i)*time.Second))
	}
	if len(cache.users) != appLanguageUsers {
		t.Fatalf("cache holds %d users", len(cache.users))
	}
	if cache.users["u"] != nil || cache.users[strings.Repeat("u", appLanguageUsers+5)] == nil {
		t.Fatal("cache did not drop the oldest users")
	}
}

func TestDSMPreference(t *testing.T) {
	for _, tc := range []struct {
		personal   string
		personalOK bool
		system     string
		systemOK   bool
		want       string
		wantOK     bool
	}{
		{"cht", true, "enu", true, "cht", true},
		{"def", true, "jpn", true, "jpn", true},
		{"", true, "jpn", true, "jpn", true},
		{"def", true, "", false, "", true},
		{"def", true, "def", true, "", true},
		{"rus", true, "enu", true, "", true},
		{"", false, "enu", true, "", false},
	} {
		got, ok := dsmPreference(tc.personal, tc.personalOK, tc.system, tc.systemOK)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("%+v: got %q %v", tc, got, ok)
		}
	}
}

// The DSM read is one compound request to loopback with the caller's own
// cookie and token; a normal user's refused system language follows the browser.
func TestReadDSMLanguage(t *testing.T) {
	for _, tc := range []struct {
		reply  string
		want   string
		wantOK bool
	}{
		{`{"success":true,"data":{"result":[{"api":"SYNO.Core.UserSettings","success":true,"data":{"Personal":{"lang":"cht"}}},{"api":"SYNO.Core.Region.Language","success":true,"data":{"language":"def"}}]}}`, "cht", true},
		{`{"success":true,"data":{"result":[{"api":"SYNO.Core.UserSettings","success":true,"data":{"Desktop":{}}},{"api":"SYNO.Core.Region.Language","success":false,"error":{"code":105}}]}}`, "", true},
		{`{"success":true,"data":{"result":[{"api":"SYNO.Core.UserSettings","success":true,"data":{"Personal":{"lang":"def"}}},{"api":"SYNO.Core.Region.Language","success":true,"data":{"language":"krn"}}]}}`, "krn", true},
		{`{"success":true,"data":{"result":[{"api":"SYNO.Core.UserSettings","success":false,"error":{"code":119}}]}}`, "", false},
		{`{"success":false,"error":{"code":119}}`, "", false},
		{`not json`, "", false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, _ := r.Cookie("id")
			if cookie == nil || cookie.Value != "session" || r.Header.Get("X-SYNO-TOKEN") != "token" {
				t.Errorf("carriers: cookie=%v token=%q", cookie, r.Header.Get("X-SYNO-TOKEN"))
			}
			r.ParseForm()
			if r.PostForm.Get("api") != "SYNO.Entry.Request" || !strings.Contains(r.PostForm.Get("compound"), "SYNO.Core.UserSettings") || !strings.Contains(r.PostForm.Get("compound"), "SYNO.Core.Region.Language") {
				t.Errorf("form = %v", r.PostForm)
			}
			w.Write([]byte(tc.reply))
		}))
		saved := dsmLanguageEndpoint
		dsmLanguageEndpoint = server.URL + "/webapi/entry.cgi"
		got, ok := readDSMLanguage(context.Background(), map[string]string{"dsm_cookie": "session", "dsm_synotoken": "token"})
		dsmLanguageEndpoint = saved
		server.Close()
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("%s: got %q %v", tc.reply, got, ok)
		}
	}
}
