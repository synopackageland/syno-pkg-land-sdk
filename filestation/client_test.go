package filestation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c, err := NewClient(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func sessionRequest() *http.Request {
	r := httptest.NewRequest("POST", "http://package/api", nil)
	r.AddCookie(&http.Cookie{Name: "id", Value: "test-session"})
	r.Header.Set("X-SYNO-TOKEN", "test-token")
	r.Header.Set("Origin", "http://package")
	return r
}
func TestReadOperationsUseOnlyCurrentSession(t *testing.T) {
	calls := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if (r.Method != "GET" && r.URL.RawQuery != "") || r.Header.Get("X-SYNO-TOKEN") != "test-token" || r.Header.Get("Cookie") != "id=test-session" {
			t.Error("incorrect session transport")
		}
		r.ParseForm()
		if r.Form.Get("_sid") != "" || (r.Method != "GET" && r.Form.Get("SynoToken") != "test-token") {
			t.Error("incorrect form carriers")
		}
		switch r.Form.Get("method") {
		case "get":
			fmt.Fprint(w, `{"success":true,"data":{"username":"alice"}}`)
		case "list_share":
			fmt.Fprint(w, `{"success":true,"data":{"shares":[{"path":"/home","name":"home","isdir":true}],"total":1}}`)
		case "list":
			if r.Form.Get("folder_path") != "/home" || r.Form.Get("limit") != "100" {
				t.Error("list fields")
			}
			fmt.Fprint(w, `{"success":true,"data":{"files":[{"path":"/home/a","name":"a","isdir":false}],"total":1}}`)
		case "getinfo":
			fmt.Fprint(w, `{"success":true,"data":{"files":[{"path":"/home/a","name":"a","isdir":false,"additional":{"size":3}}]}}`)
		case "download":
			if r.Form.Get("path") != `["/home/a"]` {
				t.Error("download path")
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="a"`)
			fmt.Fprint(w, "abc")
		default:
			t.Error("unexpected method")
		}
	})
	r := sessionRequest()
	if err := c.Verify(context.Background(), r, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Discover(context.Background(), r, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(context.Background(), r, "/home", ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stat(context.Background(), r, "/home/a"); err != nil {
		t.Fatal(err)
	}
	b, err := c.Read(context.Background(), r, "/home/a")
	if err != nil || string(b) != "abc" {
		t.Fatal(err)
	}
	if calls != 5 {
		t.Fatal(calls)
	}
}
func TestRefusalAndBoundedErrors(t *testing.T) {
	for _, tt := range []struct{ body, want string }{
		{`{"success":false,"error":{"code":119,"secret":"test-session"}}`, "session_required"},
		{`{"success":false,"error":{"code":407}}`, "denied"},
		{`{"success":false,"error":{"code":408}}`, "denied"},
		{`{"success":false,"error":{"code":1805}}`, "conflict"},
		{`{"success":false,"error":{"code":9999}}`, "unavailable"},
		{`{"data":{}}`, "unavailable"}, {`not json`, "unavailable"},
	} {
		t.Run(tt.want+tt.body, func(t *testing.T) {
			c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tt.body) })
			_, err := c.List(context.Background(), sessionRequest(), "/home", ListOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "test-session") {
				t.Fatal(err)
			}
		})
	}
}
func TestNoSessionRedirectOrRemoteDestination(t *testing.T) {
	for _, url := range []string{"https://example.com", "http://localhost:5000", "http://127.0.0.1:5000/evil", "http://user@127.0.0.1:5000", "http://127.0.0.1:5000?x=y"} {
		if _, err := NewClient(url); err == nil {
			t.Fatal("accepted", url)
		}
	}
	calls := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "http://127.0.0.1:1/leak", 302)
	})
	if _, err := c.List(context.Background(), httptest.NewRequest("POST", "/", nil), "/home", ListOptions{}); err == nil || calls != 0 {
		t.Fatal("missing session sent")
	}
	if _, err := c.List(context.Background(), sessionRequest(), "/home", ListOptions{}); err == nil || calls != 1 {
		t.Fatal("redirect accepted")
	}
}
func TestDownloadDoesNotTurnErrorsIntoFileBytes(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]int{"code": 407}})
	})
	if b, err := c.Read(context.Background(), sessionRequest(), "/home/a"); err == nil || len(b) != 0 {
		t.Fatal("error bytes escaped")
	}
}

func TestBoundariesCancellationAndIdentityMismatch(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("method") == "download" {
			w.Header().Set("Content-Disposition", `attachment; filename="large"`)
			fmt.Fprint(w, strings.Repeat("x", MaxBytes+1))
			return
		}
		fmt.Fprint(w, `{"success":true,"data":{"username":"someone-else"}}`)
	})
	if e := c.Verify(context.Background(), sessionRequest(), "alice"); e == nil {
		t.Fatal("identity mismatch admitted")
	}
	if b, e := c.Read(context.Background(), sessionRequest(), "/home/large"); e == nil || len(b) != 0 {
		t.Fatal("oversized read escaped")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.List(ctx, sessionRequest(), "/home", ListOptions{}); e == nil {
		t.Fatal("canceled request succeeded")
	}
	r := sessionRequest()
	r.AddCookie(&http.Cookie{Name: "id", Value: "other"})
	if _, e := c.List(context.Background(), r, "/home", ListOptions{}); e == nil {
		t.Fatal("ambiguous session accepted")
	}
}
