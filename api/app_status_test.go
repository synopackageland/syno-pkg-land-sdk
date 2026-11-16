package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func withLanguages(t *testing.T) *languageCache {
	t.Helper()
	cache := newLanguageCache()
	saved := defaultLanguages
	defaultLanguages = cache
	t.Cleanup(func() { defaultLanguages = saved })
	return cache
}

// A Chinese DSM in an English browser: the entry page's hint wins and is
// remembered for the user's later requests in this process.
func TestAppStatusDefaultUsesEntryHint(t *testing.T) {
	cache := withLanguages(t)
	alice := Caller{identity: Identity{User: "alice"}, authenticated: true}
	handler := AppStatusHandler("ExampleApp", "1", nil)
	for _, tc := range []struct{ params, lang, locale string }{
		{`{}`, "", "en-US"},
		{`{"dsmLanguage":"cht"}`, "cht", "zh-TW"},
		{`{}`, "cht", "zh-TW"},
		{`{"dsmLanguage":"def"}`, "", "en-US"},
		{`{"dsmLanguage":"jpn"}`, "jpn", "ja"},
		{`{"dsmLanguage":""}`, "", "en-US"},
		{`{"dsmLanguage":"rus"}`, "", "en-US"},
		{`{"dsmLanguage":"<script>"}`, "", "en-US"},
		{`not json`, "", "en-US"},
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
	if cache.users["bob"] != "ger" {
		t.Fatalf("bob = %q", cache.users["bob"])
	}
}

// app.status never contacts DSM: without a hint it answers at once from the
// browser's language, even with DSM session carriers on the request.
func TestAppStatusDefaultNeverReadsDSM(t *testing.T) {
	withLanguages(t)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("app.status contacted a server")
	}))
	defer server.Close()
	alice := Caller{identity: Identity{User: "alice"}, authenticated: true}
	if status := callStatus(t, AppStatusHandler("ExampleApp", "1", nil), alice, statusRequest("ko-KR"), `{}`); status.Locale != "ko-KR" {
		t.Fatalf("status = %+v", status)
	}
}

func TestAppLanguageCacheIsBounded(t *testing.T) {
	cache := newLanguageCache()
	for i := range appLanguageUsers + 5 {
		cache.store(strings.Repeat("u", i+1), "cht")
	}
	// Storing again moves a user to the newest end.
	cache.store(strings.Repeat("u", 6), "jpn")
	cache.store("new", "enu")
	if len(cache.users) != appLanguageUsers {
		t.Fatalf("cache holds %d users", len(cache.users))
	}
	if _, ok := cache.users[strings.Repeat("u", 7)]; ok {
		t.Fatal("cache did not drop the oldest user")
	}
	if cache.users[strings.Repeat("u", 6)] != "jpn" || cache.users["new"] != "enu" {
		t.Fatal("cache dropped a recent user")
	}
}
