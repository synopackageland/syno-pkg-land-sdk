package filestation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// identityServer answers like DSM for user "alice" with one 5-byte file.
func identityServer(t *testing.T, size int, user string) *Client {
	return fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.Form.Get("method") {
		case "get":
			fmt.Fprintf(w, `{"success":true,"data":{"username":%q}}`, user)
		case "getinfo":
			isdir := "false"
			if r.Form.Get("path") == `["/home"]` {
				isdir = "true"
			}
			fmt.Fprintf(w, `{"success":true,"data":{"files":[{"path":%s,"name":"x","isdir":%s,"additional":{"size":%d}}]}}`, r.Form.Get("path")[1:len(r.Form.Get("path"))-1], isdir, size)
		case "download":
			w.Header().Set("Content-Disposition", `attachment; filename="x"`)
			fmt.Fprint(w, "hello")
		}
	})
}

func fileService(t *testing.T, c *Client) (*Service, string) {
	s := NewService(c)
	return s, "/home"
}

func alice(t *testing.T) api.Caller {
	var got api.Caller
	srv, _ := sessionTestServer(testAuth{"alice"}, api.HandleRequest("a.b", func(_ context.Context, c api.Caller, _ *http.Request, _ json.RawMessage) (any, error) {
		got = c
		return nil, nil
	}))
	r := sessionRequest()
	r.Body = ioNop([]byte(`{"method":"a.b","params":{}}`))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func TestReadFileReturnsSelectedFileThroughCurrentSession(t *testing.T) {
	s, folder := fileService(t, identityServer(t, 5, "alice"))
	data, err := s.ReadFile(context.Background(), alice(t), sessionRequest(), folder, "docs/x.txt")
	if err != nil || string(data) != "hello" {
		t.Fatalf("%q %v", data, err)
	}
}

func TestReadFileRefusesWhenSessionBelongsToAnotherUser(t *testing.T) {
	s, folder := fileService(t, identityServer(t, 5, "mallory"))
	if _, err := s.ReadFile(context.Background(), alice(t), sessionRequest(), folder, "x.txt"); err == nil {
		t.Fatal("a session that resolves to another user must be refused")
	}
}

func TestReadFileRefusesNonFoldersTraversalAndOversize(t *testing.T) {
	s, folder := fileService(t, identityServer(t, MaxBytes+1, "alice"))
	ctx, c := context.Background(), alice(t)
	if _, err := s.ReadFile(ctx, c, sessionRequest(), folder, "x.txt"); err == nil {
		t.Fatal("oversize file must be refused before download")
	}
	if _, err := s.ReadFile(ctx, c, sessionRequest(), "/home/x.txt", "x.txt"); err == nil {
		t.Fatal("a folder that is not a directory must be refused")
	}
	if _, err := s.ReadFile(ctx, c, sessionRequest(), folder, "../etc/passwd"); err == nil {
		t.Fatal("traversal must be refused")
	}
	if _, err := s.ReadFile(ctx, c, sessionRequest(), folder, ""); err == nil {
		t.Fatal("the selected folder itself is not a file")
	}
}

func TestReadFileOnNilServiceIsUnavailable(t *testing.T) {
	var s *Service
	if _, err := s.ReadFile(context.Background(), api.Caller{}, sessionRequest(), "x", "y"); err == nil {
		t.Fatal("nil service must refuse")
	}
}
