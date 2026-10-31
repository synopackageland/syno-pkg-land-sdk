package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"not_found", 404}, {"source_deleted", 404}, {"unknown_method", 404},
		{"forbidden", 403}, {"permission", 403}, {"permission_denied", 403}, {"denied", 403}, {"admin_required", 403}, {"app_privilege_denied", 403}, {"cross_origin", 403}, {"origin_required", 403},
		{"unauthorized", 401}, {"session_required", 401}, {"bad_request", 400},
		{"request_too_large", 413}, {"auth_unavailable", 503}, {"unavailable", 503}, {"request_timeout", 503},
		{"internal", 500}, {"custom_error", 500},
	} {
		t.Run(tc.code, func(t *testing.T) {
			srv := newTestServer(t, &fakeAuth{user: "alice", admin: true}, HandleStream("demo.file", func(context.Context, Caller, http.ResponseWriter, *http.Request) error {
				return fmt.Errorf("wrapped: %w", WithDetails(&Error{tc.code, "refused"}, map[string]bool{"retry": false}))
			}))
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest("GET", "/api.cgi?method=demo.file", nil))
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || !strings.Contains(w.Body.String(), `"details":{"retry":false}`) {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Disposition") != "" {
				t.Fatal(w.Header())
			}
		})
	}
}

func TestStreamErrorsBeforeAndAfterCommit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler StreamHandler
		status  int
		body    string
	}{
		{"untyped", func(context.Context, Caller, http.ResponseWriter, *http.Request) error {
			return errors.New("private path")
		}, 500, `{"error":{"code":"internal","message":"internal error"},"success":false}` + "\n"},
		{"panic", func(context.Context, Caller, http.ResponseWriter, *http.Request) error { panic("secret") }, 500, `{"error":{"code":"internal","message":"internal error"},"success":false}` + "\n"},
		{"partial", func(_ context.Context, _ Caller, w http.ResponseWriter, _ *http.Request) error {
			w.Write([]byte("file"))
			return errors.New("broken")
		}, 200, "file"},
		{"headers", func(_ context.Context, _ Caller, w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(206)
			return BadRequest("late")
		}, 206, ""},
		{"prepared", func(_ context.Context, _ Caller, w http.ResponseWriter, _ *http.Request) error {
			w.Header().Set("Content-Disposition", "attachment; filename=a.txt")
			w.Header().Set("Content-Length", "99")
			return BadRequest("early")
		}, 400, `{"error":{"code":"bad_request","message":"early"},"success":false}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			newTestServer(t, &fakeAuth{admin: true}, HandleStream("demo.file", tc.handler)).ServeHTTP(w, httptest.NewRequest("GET", "/api.cgi?method=demo.file", nil))
			if w.Code != tc.status || w.Body.String() != tc.body {
				t.Fatalf("%d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestStreamPanicsBeforeAndAfterCommit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		panic any
		abort bool
	}{
		{"abort", http.ErrAbortHandler, true},
		{"real panic", "boom", false},
	} {
		for _, started := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/started=%t", tc.name, started), func(t *testing.T) {
				var logs bytes.Buffer
				output := log.Writer()
				log.SetOutput(&logs)
				t.Cleanup(func() { log.SetOutput(output) })

				srv := newTestServer(t, &fakeAuth{admin: true}, HandleStream("demo.file", func(_ context.Context, _ Caller, w http.ResponseWriter, _ *http.Request) error {
					if started {
						w.Write([]byte("file"))
					}
					panic(tc.panic)
				}))
				w := httptest.NewRecorder()
				var recovered any
				func() {
					defer func() { recovered = recover() }()
					srv.ServeHTTP(w, httptest.NewRequest("GET", "/api.cgi?method=demo.file", nil))
				}()
				if tc.abort {
					if recovered != http.ErrAbortHandler {
						t.Fatalf("recovered = %v, want http.ErrAbortHandler", recovered)
					}
					if logs.Len() != 0 {
						t.Fatalf("abort logged: %s", &logs)
					}
				} else {
					if recovered != nil {
						t.Fatalf("real panic escaped: %v", recovered)
					}
					if !strings.Contains(logs.String(), "handler panic: boom") {
						t.Fatalf("missing panic log: %s", &logs)
					}
					transferFailed := strings.Contains(logs.String(), "transfer failed after response started: internal: internal error")
					if transferFailed != started {
						t.Fatalf("transfer failure log: started=%t logs=%s", started, &logs)
					}
				}
				status, body := http.StatusOK, ""
				if started {
					body = "file"
				} else if !tc.abort {
					status = http.StatusInternalServerError
					body = `{"error":{"code":"internal","message":"internal error"},"success":false}` + "\n"
				}
				if w.Code != status || w.Body.String() != body {
					t.Fatalf("response = %d %q, want %d %q", w.Code, w.Body.String(), status, body)
				}
			})
		}
	}
}

func TestStreamAuthorizationUsesHTTPStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		auth   *fakeAuth
		target string
		status int
	}{
		{"session", &fakeAuth{err: ErrNoSession}, "demo.file", 401}, {"admin", &fakeAuth{user: "alice"}, "demo.file", 403}, {"unknown", &fakeAuth{user: "alice", admin: true}, "missing", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			newTestServer(t, tc.auth, HandleStream("demo.file", func(context.Context, Caller, http.ResponseWriter, *http.Request) error {
				t.Fatal("refused handler ran")
				return nil
			})).ServeHTTP(w, httptest.NewRequest("GET", "/api.cgi?method="+tc.target, nil))
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
}
