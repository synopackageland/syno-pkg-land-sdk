package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"github.com/synopackageland/syno-pkg-land-sdk/secret"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRequestFailureLogsExcludeUntrustedValues(t *testing.T) {
	for _, mode := range []string{"error", "panic", "timeout", "observer"} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			old := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(old)
			srv := newTestServer(t, &fakeAuth{admin: true}, Handle("demo.crash", func(context.Context, Caller, json.RawMessage) (any, error) {
				if mode == "panic" {
					panic("unlabelled-canary")
				}
				if mode == "timeout" {
					return nil, context.DeadlineExceeded
				}
				return nil, errors.New("unlabelled-canary")
			}))
			var handler http.Handler = srv
			if mode == "observer" {
				handler = WithObserver(srv, func(*http.Request, Outcome) { panic("unlabelled-canary") })
			}
			rec, _ := post(t, handler, `{"method":"demo.crash","params":{"password":"unlabelled-canary"}}`)
			if strings.Contains(logs.String()+rec.Body.String(), "unlabelled-canary") {
				t.Fatalf("leaked: %s %s", &logs, rec.Body)
			}
		})
	}
}

func TestObserverCannotDumpCredentials(t *testing.T) {
	srv := newTestServer(t, &fakeAuth{admin: true}, Handle("demo.crash", func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil }))
	handler := WithObserver(srv, func(r *http.Request, _ Outcome) {
		if r.Body != http.NoBody || r.Header.Get("Authorization") != "[redacted]" || r.URL.RawQuery != "" || r.Header.Get("X-SYNO-TOKEN") != "[redacted]" {
			t.Fatal("observer received raw request")
		}
		if r.Header.Get("Sidebar") != "left" || r.Header.Get("TokenCount") != "7" {
			t.Fatal("observer redacted non-credential headers")
		}
	})
	post(t, handler, `{"method":"demo.crash","params":{"password":"canary"}}`, func(r *http.Request) {
		r.URL.RawQuery = "token=canary"
		r.Header.Set("Authorization", "Bearer canary")
		r.Header.Set("X-SYNO-TOKEN", "canary")
		r.Header.Set("Sidebar", "left")
		r.Header.Set("TokenCount", "7")
	})
}

func TestOriginFallbackStrictSyntaxAndHTTPCompatibility(t *testing.T) {
	for _, tc := range []struct {
		source string
		ok     bool
	}{
		{"http://app.example:8080", true}, {"https://app.example:8080", true},
		{"ftp://app.example:8080", false}, {"//app.example:8080", false}, {"https://user:pass@app.example:8080", false},
		{"https://app.example:8080/path", false}, {"https://app.example:8080?token=canary", false},
		{"https://app.example:8080#fragment", false}, {"http://app.example:8081", false}, {"null", false},
	} {
		r := httptest.NewRequest("POST", "http://app.example:8080/api", nil)
		r.Header.Set("Origin", tc.source)
		if got := CheckOrigin(r); (got == nil) != tc.ok {
			t.Errorf("%q accepted=%v", tc.source, got == nil)
		}
	}
}

func TestDeclaredRequestSecretsPreserveErrorAndRefuseReplay(t *testing.T) {
	for _, batch := range []bool{false, true} {
		dir := t.TempDir() + "/spool"
		route := WithRequestSecrets(Handle("demo.crash", func(ctx context.Context, _ Caller, raw json.RawMessage) (any, error) {
			store := background.NewStore(dir)
			_, _, err := store.Submit(ctx, background.SubmitRequest{Key: "request", Name: "demo", Data: raw, Submitter: background.Submitter{User: "fixture", UID: "1001"}})
			if !errors.Is(err, secret.ErrPersistence) {
				t.Fatalf("persistent store accepted secret scope: %v", err)
			}
			return nil, WithDetails(BadRequest("external refused custom-canary"), map[string]any{"password": "canary-p", "echo": "custom-canary", "safe": "visible"})
		}), "credential")
		srv := newTestServer(t, &fakeAuth{admin: true}, route)
		body := `{"method":"demo.crash","params":{"credential":"custom-canary"}}`
		if batch {
			body = `{"v":1,"calls":[` + body + `]}`
		}
		rec, _ := post(t, srv, body)
		if !strings.Contains(rec.Body.String(), "custom-canary") || !strings.Contains(rec.Body.String(), "canary-p") || !strings.Contains(rec.Body.String(), "visible") {
			t.Fatal(rec.Body.String())
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("store touched disk before refusing credentials")
		}
	}
}

func TestDeclaredRequestSecretSuccessPreservesNumbers(t *testing.T) {
	route := WithRequestSecrets(Handle("demo.crash", func(context.Context, Caller, json.RawMessage) (any, error) {
		return map[string]any{"id": json.Number("9007199254740993"), "echo": "custom-canary", "token": "canary-t"}, nil
	}), "credential")
	rec, _ := post(t, newTestServer(t, &fakeAuth{admin: true}, route), `{"method":"demo.crash","params":{"credential":"custom-canary"}}`)
	if !strings.Contains(rec.Body.String(), "custom-canary") || !strings.Contains(rec.Body.String(), "canary-t") || !strings.Contains(rec.Body.String(), "9007199254740993") {
		t.Fatal(rec.Body.String())
	}
}

func TestOriginFallbackRefererPortAndSchemeWarning(t *testing.T) {
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	for _, tc := range []struct {
		source    string
		forwarded string
		ok        bool
	}{
		{"http://app.example:8080/page?view=one", "", true},
		{"https://app.example:8080/page", "", true},
		{"https://app.example:8081/page", "", false},
		{"https://proxy.example:8443/page", "proxy.example:8443", true},
		{"https://proxy.example:8443/page", "proxy.example:443", false},
		{"https://user:pass@app.example:8080/page", "", false},
	} {
		r := httptest.NewRequest("POST", "http://app.example:8080/api", nil)
		r.Header.Set("Referer", tc.source)
		r.Header.Set("X-Forwarded-Host", tc.forwarded)
		if got := CheckOrigin(r); (got == nil) != tc.ok {
			t.Errorf("referer accepted=%v want=%v", got == nil, tc.ok)
		}
	}
	if !strings.Contains(logs.String(), "HTTP remains allowed") || strings.Contains(logs.String(), "app.example") {
		t.Fatal("missing safe scheme warning")
	}
	r := httptest.NewRequest("POST", "http://app.example:8080/api", nil)
	r.Header.Add("Origin", "http://app.example:8080")
	r.Header.Add("Origin", "http://other.example")
	if CheckOrigin(r) == nil {
		t.Fatal("multiple origins accepted")
	}
}

func TestDeclaredRequestSecretErrorsPreserveSessionHeaders(t *testing.T) {
	route := WithRequestSecrets(HandleRequest("demo.crash", func(_ context.Context, _ Caller, r *http.Request, _ json.RawMessage) (any, error) {
		return nil, BadRequest(r.Header.Get("X-SYNO-TOKEN") + " bearer-canary cookie-canary")
	}), "credential")
	rec, _ := post(t, newTestServer(t, &fakeAuth{admin: true}, route), `{"method":"demo.crash","params":{}}`, func(r *http.Request) {
		r.Header.Set("X-SYNO-TOKEN", "session-canary")
		r.Header.Set("Authorization", "Bearer bearer-canary")
		r.AddCookie(&http.Cookie{Name: "session", Value: "cookie-canary"})
	})
	if !strings.Contains(rec.Body.String(), "session-canary bearer-canary cookie-canary") {
		t.Fatal(rec.Body.String())
	}
}

func TestClientResponsesPreserveFieldNames(t *testing.T) {
	payload := map[string]any{"token": "t", "password": "p", "sid": "s", "tokenCount": 7, "sidebar": "left", "secretName": "label", "otpEnabled": true, "api_token": "custom", "nested": []any{map[string]any{"password": "nested"}}}
	expected, _ := json.Marshal(payload)
	for _, kind := range []string{"rpc", "batch", "stream"} {
		for _, failure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/error=%t", kind, failure), func(t *testing.T) {
				public := &Error{Code: "token_count", Message: "password sid tokenCount sidebar"}
				route := Handle("demo.fields", func(context.Context, Caller, json.RawMessage) (any, error) {
					if failure {
						return nil, WithDetails(public, payload)
					}
					return payload, nil
				})
				if kind == "stream" {
					if !failure {
						return
					}
					route = HandleStream("demo.fields", func(context.Context, Caller, http.ResponseWriter, *http.Request) error {
						return WithDetails(public, payload)
					})
				}
				srv := newTestServer(t, &fakeAuth{admin: true}, route)
				var rec *httptest.ResponseRecorder
				if kind == "stream" {
					rec = httptest.NewRecorder()
					srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api.cgi?method=demo.fields", nil))
				} else {
					body := `{"method":"demo.fields","params":{}}`
					if kind == "batch" {
						body = `{"v":1,"calls":[` + body + `]}`
					}
					rec, _ = post(t, srv, body)
				}
				if !strings.Contains(rec.Body.String(), string(expected)) {
					t.Fatal(rec.Body.String())
				}
				if failure && (!strings.Contains(rec.Body.String(), public.Code) || !strings.Contains(rec.Body.String(), public.Message)) {
					t.Fatal(rec.Body.String())
				}
			})
		}
	}
}
