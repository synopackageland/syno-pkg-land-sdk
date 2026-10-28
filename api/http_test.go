package api

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"
)

const httpPolicy = `{"schema":1,"default":"administrators","methods":{"app.proxy":"authenticated"}}`

func TestHTTPRoutesEntryIdentityPermissionAndOrigin(t *testing.T) {
	for _, tc := range []struct {
		mode, permission, user string
		admin                  bool
		authError              error
		status                 int
	}{
		{"admin-only", "authenticated", "ordinary", false, nil, 403},
		{"admin-only", "authenticated", "operator", true, nil, 200},
		{"app-privilege", "authenticated", "ordinary", false, nil, 200},
		{"app-privilege", "authenticated", "", false, ErrNoSession, 401},
		{"app-privilege", "authenticated", "ordinary", false, ErrAppPrivilegeDenied, 403},
		{"app-privilege", "administrators", "ordinary", false, nil, 403},
		{"", "authenticated", "", false, ErrNoSession, 401},
	} {
		t.Run(fmt.Sprintf("%s/%s/%s/%d", tc.mode, tc.permission, tc.user, tc.status), func(t *testing.T) {
			auth := entryAuth{&fakeAuth{user: tc.user, admin: tc.admin, err: tc.authError}, tc.mode}
			calls := 0
			srv, err := NewServerWithPermissions(auth, []byte(strings.Replace(httpPolicy, "authenticated", tc.permission, 1)),
				HandleHTTP("/proxy/", "app.proxy", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					c := HTTPCaller(r.Context())
					if !c.Authenticated() || c.User() != tc.user {
						t.Errorf("lost verified caller: %+v", c)
					}
					data, _ := io.ReadAll(r.Body)
					w.Write(data)
				})))
			if err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
				r := httptest.NewRequest(method, "/proxy/file?method=app.ignored", strings.NewReader("untouched"))
				r.Header.Set("Origin", "http://example.com")
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				if w.Code != tc.status {
					t.Fatalf("%s status=%d body=%s", method, w.Code, w.Body)
				}
				if tc.status == 200 && w.Body.String() != "untouched" {
					t.Fatal("raw body consumed before dispatch")
				}
			}
			before := calls
			for _, target := range []string{"/proxy/a/../secret", "/proxy//secret", "/proxy/%2fsecret", "/proxy/%00secret", "/proxy/a%20b", "/proxy/%252e%252e/secret"} {
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
				if w.Code != 400 {
					t.Fatalf("noncanonical %s: %d", target, w.Code)
				}
			}
			r := httptest.NewRequest("GET", "/proxy/secret", nil)
			r.Header.Set("Origin", "https://other.example")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			if w.Code != 403 || calls != before {
				t.Fatal("origin or canonical-path check executed handler")
			}
			w, _ = post(t, srv, `{"v":1,"calls":[{"method":"app.proxy"}]}`)
			if calls != before || !strings.Contains(w.Body.String(), `"success":false`) {
				t.Fatal("raw handler entered through batch")
			}
			post(t, srv, `{"method":"app.proxy"}`)
			srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/proxySibling/", nil))
			if calls != before {
				t.Fatal("raw handler entered through RPC or partial segment")
			}
		})
	}
}

func TestHTTPRouteRegistrationAndBuildBinding(t *testing.T) {
	h := http.NotFoundHandler()
	for _, prefix := range []string{"", "/", "/proxy", "/_sso/", "/_spl/sub/", "/api/", "/rpc/", "/api.cgi/", "/webman/", "/cgi/", "/proxy/../", "/proxy/%2f/"} {
		if _, err := NewServerWithPermissions(&fakeAuth{}, []byte(httpPolicy), HandleHTTP(prefix, "app.proxy", h)); err == nil {
			t.Errorf("invalid prefix accepted: %q", prefix)
		}
	}
	if _, err := NewServerWithPermissions(&fakeAuth{}, []byte(strings.Replace(httpPolicy, "authenticated", "public", 1)), HandleHTTP("/proxy/", "app.proxy", h)); err == nil {
		t.Fatal("anonymous raw handler accepted")
	}
	if _, err := NewServerWithPermissions(&fakeAuth{}, []byte(emptyAppPolicy), HandleHTTP("/proxy/", "app.proxy", h)); err == nil {
		t.Fatal("undeclared raw handler accepted")
	}
	overlapping := []byte(`{"schema":1,"default":"administrators","methods":{"app.proxy":"authenticated","app.other":"authenticated"}}`)
	if _, err := NewServerWithPermissions(&fakeAuth{}, overlapping, HandleHTTP("/proxy/", "app.proxy", h), HandleHTTP("/proxy/sub/", "app.other", h)); err == nil {
		t.Fatal("overlapping prefixes accepted")
	}
	described, err := DescribeRoutes([]byte(httpPolicy), HandleHTTP("/proxy/", "app.proxy", h))
	if err != nil || len(described) != 1 || described[0]["kind"] != "http" || described[0]["prefix"] != "/proxy/" {
		t.Fatalf("raw route metadata lost: %v %v", described, err)
	}
	t.Cleanup(func() { expectedHTTPRoutesDigest = "" })
	expectedHTTPRoutesDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(`[{"prefix":"/proxy/","method":"app.proxy"}]`)))
	if _, err := NewServerWithPermissions(&fakeAuth{}, []byte(httpPolicy), HandleHTTP("/proxy/", "app.proxy", h)); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServerWithPermissions(&fakeAuth{}, []byte(httpPolicy), HandleHTTP("/other/", "app.proxy", h)); err == nil {
		t.Fatal("prefix change not bound")
	}
	if _, err := NewServerWithPermissions(&fakeAuth{}, []byte(httpPolicy), Handle("app.proxy", func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil })); err == nil {
		t.Fatal("HTTP inventory removed")
	}
}

func TestHTTPRoutesUseCGIPathInfoWithoutChangingTheOriginalURL(t *testing.T) {
	t.Setenv("GATEWAY_INTERFACE", "CGI/1.1")
	const script = "/webman/3rdparty/SampleApp/cgi/api.cgi"
	t.Setenv("SCRIPT_NAME", script)
	calls := 0
	srv, err := NewServerWithPermissions(&fakeAuth{user: "operator"}, []byte(httpPolicy),
		HandleHTTP("/proxy/", "app.proxy", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path != script+"/proxy/item" {
				t.Fatal("original request URL changed")
			}
			w.WriteHeader(204)
		})))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", script+"/proxy/item", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if calls != 1 || w.Code != 204 {
		t.Fatalf("CGI path info: calls=%d status=%d %s", calls, w.Code, w.Body)
	}
	if srv.HTTPRoute(httptest.NewRequest("GET", script, nil)) != "" {
		t.Fatal("RPC script path selected a raw handler")
	}
}

func TestHTTPStreamingAndWebSocketUpgrade(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			accept := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
			fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:]))
			rw.Flush()
			data := make([]byte, 6)
			if _, err := io.ReadFull(rw, data); err == nil && data[0] == 0x89 && data[1] == 0x80 {
				rw.Write([]byte{0x8a, 0})
				rw.Flush()
			}
			return
		}
		io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		io.WriteString(w, "second")
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	srv, err := NewServerWithPermissions(&fakeAuth{user: "operator", admin: true}, []byte(httpPolicy),
		HandleHTTP("/proxy/", "app.proxy", proxy))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(srv)
	defer server.Close()
	response, err := http.Get(server.URL + "/proxy/stream")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(data) != "firstsecond" || len(response.TransferEncoding) == 0 {
		t.Fatal("streaming framing lost")
	}
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET /proxy/ws?method=ignored HTTP/1.1\r\nHost: example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	reader := bufio.NewReader(conn)
	upgrade, err := http.ReadResponse(reader, nil)
	if err != nil || upgrade.StatusCode != 101 || upgrade.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("upgrade: %v %v", upgrade, err)
	}
	conn.Write([]byte{0x89, 0x80, 1, 2, 3, 4})
	pong := make([]byte, 2)
	if _, err := io.ReadFull(reader, pong); err != nil || pong[0] != 0x8a {
		t.Fatalf("upgrade stream: %x %v", pong, err)
	}
}
