//go:build unix

package bridge

import (
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

// transferServer serves demo.upload and demo.download from app, spooling in
// a fresh data directory.
func transferServer(t *testing.T, app fakeApp, max int64) (http.Handler, string) {
	t.Helper()
	spool := filepath.Join(t.TempDir(), "bridge-spool")
	s := &Session{Dial: pipeDialer(app), Methods: app.methods, Spool: spool, MaxUploadBytes: max,
		Transfers: map[string]string{"demo.upload": "upload", "demo.download": "download"}}
	policy := `{"schema":1,"default":"administrators","methods":{"demo.upload":"authenticated","demo.download":"authenticated"}}`
	srv, err := api.NewServerWithPermissions(userAuth{api.Identity{User: "alice"}}, []byte(policy), Routes(s)...)
	if err != nil {
		t.Fatal(err)
	}
	return srv, spool
}

func transfer(srv http.Handler, method, target, contentType, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://nas.local:5001/api.cgi"+target, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	r.Header.Set("Origin", "https://nas.local:5001")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

func spoolEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAnUploadReachesTheAppAsASpooledFile(t *testing.T) {
	var params map[string]any
	var content string
	var mode os.FileMode
	app := fakeApp{methods: []string{"demo.upload", "demo.download"}, handle: func(_ *fakeConn, call map[string]any) map[string]any {
		params = call["params"].(map[string]any)
		path := params["upload"].(map[string]any)["path"].(string)
		b, _ := os.ReadFile(path)
		content = string(b)
		info, _ := os.Stat(path)
		mode = info.Mode().Perm()
		return map[string]any{"ok": true, "data": map[string]any{"saved": "<b>"}}
	}}
	srv, spool := transferServer(t, app, 0)
	w := transfer(srv, "POST", "?method=demo.upload&name=a.txt&SynoToken=secret", "application/octet-stream", "hello")
	if w.Body.String() != "{\"data\":{\"saved\":\"\\u003cb\\u003e\"},\"success\":true}\n" || w.Header().Get("Content-Length") != "50" {
		t.Fatalf("reply %q %v", w.Body.String(), w.Header())
	}
	upload := params["upload"].(map[string]any)
	query := params["query"].(map[string]any)
	if content != "hello" || upload["size"] != float64(5) || len(query) != 1 || query["name"] != "a.txt" || mode != 0o600 {
		t.Fatalf("params %v content %q mode %v", params, content, mode)
	}
	if !strings.HasPrefix(upload["path"].(string), spool+"/") {
		t.Fatalf("path %v outside %s", upload["path"], spool)
	}
	if info, err := os.Stat(spool); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("spool %v %v", info, err)
	}
	if left := spoolEntries(t, spool); len(left) != 0 {
		t.Fatalf("spool kept %v", left)
	}
}

func TestUploadsAreRefusedBeforeTheApp(t *testing.T) {
	reached := false
	app := fakeApp{methods: []string{"demo.upload", "demo.download"}, handle: func(*fakeConn, map[string]any) map[string]any {
		reached = true
		return map[string]any{"ok": true}
	}}
	srv, _ := transferServer(t, app, 4)
	for _, c := range []struct{ method, target, contentType, body, code string }{
		{"POST", "?method=demo.upload", "application/octet-stream", "hello", "request_too_large"},
		{"POST", "?method=demo.upload", "text/plain", "hi", "bad_request"},
		{"GET", "?method=demo.upload", "", "", "method_not_allowed"},
		{"POST", "?method=demo.upload&a=1&a=2", "application/octet-stream", "hi", "bad_request"},
	} {
		w := transfer(srv, c.method, c.target, c.contentType, c.body)
		if !strings.Contains(w.Body.String(), `"code":"`+c.code+`"`) {
			t.Errorf("%s %s: %s", c.method, c.target, w.Body)
		}
	}
	r := httptest.NewRequest("POST", "https://nas.local:5001/api.cgi?method=demo.upload", io.MultiReader(strings.NewReader("hi")))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("Origin", "https://nas.local:5001")
	r.ContentLength = -1
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `"code":"bad_request"`) || reached {
		t.Fatalf("unknown length: %s reached %v", w.Body, reached)
	}
}

func TestADownloadStreamsTheFileTheAppWrote(t *testing.T) {
	var params map[string]any
	app := fakeApp{methods: []string{"demo.upload", "demo.download"}, handle: func(_ *fakeConn, call map[string]any) map[string]any {
		params = call["params"].(map[string]any)
		os.WriteFile(params["download"].(map[string]any)["path"].(string), []byte("a,b\n"), 0o600)
		return map[string]any{"ok": true, "data": map[string]any{"name": "report é.csv", "content_type": "text/csv"}}
	}}
	srv, spool := transferServer(t, app, 0)
	w := transfer(srv, "GET", "?method=demo.download&id=7", "", "")
	h := w.Header()
	if w.Body.String() != "a,b\n" || h.Get("Content-Length") != "4" || h.Get("Content-Type") != "text/csv" ||
		h.Get("Content-Disposition") != `attachment; filename*=utf-8''report%20%C3%A9.csv` || h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("reply %q %v", w.Body, h)
	}
	if params["query"].(map[string]any)["id"] != "7" {
		t.Fatalf("params %v", params)
	}
	if left := spoolEntries(t, spool); len(left) != 0 {
		t.Fatalf("spool kept %v", left)
	}
}

func TestADownloadTheAppRefusedIsAnError(t *testing.T) {
	app := fakeApp{methods: []string{"demo.upload", "demo.download"}, handle: func(_ *fakeConn, call map[string]any) map[string]any {
		path := call["params"].(map[string]any)["download"].(map[string]any)["path"].(string)
		os.WriteFile(path, []byte("partial"), 0o600)
		return map[string]any{"ok": false, "error": map[string]any{"code": "not_found", "message": "no report"}}
	}}
	srv, spool := transferServer(t, app, 0)
	w := transfer(srv, "GET", "?method=demo.download", "", "")
	if w.Body.String() != `{"error":{"code":"not_found","message":"no report"},"success":false}`+"\n" {
		t.Fatalf("reply %q", w.Body)
	}
	if left := spoolEntries(t, spool); len(left) != 0 {
		t.Fatalf("spool kept %v", left)
	}
}

func TestADownloadOnlyStreamsARegularFileAtItsSpoolPath(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte("secret"), 0o600)
	for name, write := range map[string]func(path string) map[string]any{
		"symlink": func(path string) map[string]any {
			os.Remove(path)
			os.Symlink(secret, path)
			return map[string]any{"name": "a"}
		},
		"folder": func(path string) map[string]any {
			os.Remove(path)
			os.Mkdir(path, 0o700)
			return map[string]any{"name": "a"}
		},
		"no name":       func(string) map[string]any { return map[string]any{} },
		"name with /":   func(string) map[string]any { return map[string]any{"name": "../a"} },
		"bad type":      func(string) map[string]any { return map[string]any{"name": "a", "content_type": "text/html\r\nX: y"} },
		"unknown field": func(string) map[string]any { return map[string]any{"name": "a", "path": secret} },
	} {
		app := fakeApp{methods: []string{"demo.upload", "demo.download"}, handle: func(_ *fakeConn, call map[string]any) map[string]any {
			return map[string]any{"ok": true, "data": write(call["params"].(map[string]any)["download"].(map[string]any)["path"].(string))}
		}}
		srv, spool := transferServer(t, app, 0)
		w := transfer(srv, "GET", "?method=demo.download", "", "")
		if w.Body.String() != `{"error":{"code":"internal","message":"internal error"},"success":false}`+"\n" {
			t.Errorf("%s: %q", name, w.Body)
		}
		if left := spoolEntries(t, spool); len(left) != 0 {
			t.Errorf("%s: spool kept %v", name, left)
		}
	}
}

func TestTheSpoolSweepsWhatAKilledCallLeft(t *testing.T) {
	app := fakeApp{methods: []string{"demo.upload", "demo.download"}, handle: func(*fakeConn, map[string]any) map[string]any {
		return map[string]any{"ok": true}
	}}
	srv, spool := transferServer(t, app, 0)
	os.MkdirAll(spool, 0o700)
	old, fresh := filepath.Join(spool, "old.upload"), filepath.Join(spool, "fresh.upload")
	os.WriteFile(old, nil, 0o600)
	os.WriteFile(fresh, nil, 0o600)
	stale := time.Now().Add(-StaleSpool - time.Minute)
	os.Chtimes(old, stale, stale)
	transfer(srv, "POST", "?method=demo.upload", "application/octet-stream", "x")
	if left := spoolEntries(t, spool); len(left) != 1 || left[0] != "fresh.upload" {
		t.Fatalf("spool %v", left)
	}
}

func TestASpoolThatIsASymlinkIsRefused(t *testing.T) {
	app := fakeApp{methods: []string{"demo.upload", "demo.download"}, handle: func(*fakeConn, map[string]any) map[string]any {
		return map[string]any{"ok": true}
	}}
	srv, spool := transferServer(t, app, 0)
	os.Symlink(t.TempDir(), spool)
	w := transfer(srv, "POST", "?method=demo.upload", "application/octet-stream", "x")
	if !strings.Contains(w.Body.String(), `"code":"internal"`) {
		t.Fatalf("reply %q", w.Body)
	}
}
