package filestation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testAuth struct{ user string }

func (a testAuth) Authenticate(*http.Request) (api.Identity, error) {
	if a.user == "" {
		return api.Identity{}, api.ErrNoSession
	}
	return api.Identity{User: a.user}, nil
}
func invoke(t *testing.T, s *Service, user, method, params string) map[string]any {
	t.Helper()
	server, e := sessionTestServer(testAuth{user}, s.Routes()...)
	if e != nil {
		t.Fatal(e)
	}
	r := sessionRequest()
	r.Body = http.NoBody
	b := []byte(fmt.Sprintf(`{"method":%q,"params":%s}`, method, params))
	r.Body = ioNop(b)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	var out map[string]any
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal(w.Body.String())
	}
	return out
}

// SDK file routes use immutable authenticated policies. App test routes
// explicitly opt in here.
func sessionTestServer(auth api.Authenticator, routes ...api.Route) (*api.Server, error) {
	methods := map[string]string{}
	for _, route := range routes {
		if !strings.HasPrefix(route.Method(), "nas.") {
			methods[route.Method()] = "authenticated"
		}
	}
	policy, err := json.Marshal(map[string]any{"schema": 1, "default": "administrators", "methods": methods})
	if err != nil {
		return nil, err
	}
	return api.NewServerWithPermissions(auth, policy, routes...)
}
func ioNop(b []byte) io.ReadCloser { return io.NopCloser(bytes.NewReader(b)) }
func callerFor(t *testing.T, user string) api.Caller {
	var got api.Caller
	server, err := sessionTestServer(testAuth{user}, api.HandleRequest("test.caller", func(_ context.Context, c api.Caller, _ *http.Request, _ json.RawMessage) (any, error) {
		got = c
		return nil, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	r := sessionRequest()
	r.Body = ioNop([]byte(`{"method":"test.caller","params":{}}`))
	r.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(httptest.NewRecorder(), r)
	return got
}
