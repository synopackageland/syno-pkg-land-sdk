package filestation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type transferClient struct {
	DSMClient
	read          int64
	maxChunk      int
	verified      string
	downloadToken string
}

func (c *transferClient) Verify(_ context.Context, r *http.Request, user string) error {
	c.verified = user
	if _, err := r.Cookie("id"); err != nil {
		return fail("dsm_session_required")
	}
	return nil
}
func (c *transferClient) UploadStream(_ context.Context, r *http.Request, parent, name string, source io.Reader, size int64) error {
	if parent != "/home" || name != "large.bin" {
		return fail("invalid-request")
	}
	buf := make([]byte, 32<<10)
	for {
		n, err := source.Read(buf)
		if n > c.maxChunk {
			c.maxChunk = n
		}
		c.read += int64(n)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if c.read != size {
		return fail("invalid-request")
	}
	return nil
}
func (c *transferClient) DownloadStream(_ context.Context, r *http.Request, _ string) (Download, error) {
	c.downloadToken = r.Header.Get("X-SYNO-TOKEN")
	return Download{Body: io.NopCloser(bytes.NewBufferString("contents")), ContentLength: 8, ContentType: "text/plain"}, nil
}

type largeReader struct{ remaining int64 }

func (r *largeReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.remaining {
		n = r.remaining
	}
	clear(p[:n])
	r.remaining -= n
	return int(n), nil
}
func transferRequest(method, url string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, url, body)
	r.AddCookie(&http.Cookie{Name: "id", Value: "user-session"})
	r.Header.Set("X-SYNO-TOKEN", "token")
	r.Header.Set("Origin", "http://example.com")
	return r
}
func transferServer(t *testing.T, c *transferClient) *api.Server {
	t.Helper()
	s, err := api.NewServerWithPermissions(testAuth{"fixtureuser1"}, []byte(`{"schema":1,"default":"administrators","methods":{}}`), NewService(c).Routes()...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestBrowserUploadStreams64MiB(t *testing.T) {
	c := &transferClient{}
	s := transferServer(t, c)
	r := transferRequest("POST", "http://example.com/api.cgi?method=nas.files.upload&parent=/home&name=large.bin", &largeReader{64 << 20})
	r.ContentLength = 64 << 20
	r.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	s.ServeHTTP(w, r)
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("64 MiB upload allocated %d bytes (limit 8 MiB)", allocated)
	}
	if c.read != 64<<20 || c.maxChunk > 32<<10 || c.verified != "fixtureuser1" {
		t.Fatalf("not streamed: %+v %s", c, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"success":true`)) {
		t.Fatal(w.Body.String())
	}
}
func TestBrowserDownloadDispositionAndCentralGuards(t *testing.T) {
	for _, tc := range []struct{ name, origin, user, code string }{{"normal", "http://example.com", "fixtureuser1", ""}, {"anonymous", "http://example.com", "", "session_required"}, {"csrf", "http://evil", "fixtureuser1", "cross_origin"}} {
		t.Run(tc.name, func(t *testing.T) {
			c := &transferClient{}
			s, err := api.NewServerWithPermissions(testAuth{tc.user}, []byte(`{"schema":1,"default":"administrators","methods":{}}`), NewService(c).Routes()...)
			if err != nil {
				t.Fatal(err)
			}
			r := transferRequest("GET", "http://example.com/api.cgi?method=nas.files.download&path=/home/%E5%A0%B1%E5%91%8A.txt", nil)
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if tc.code != "" {
				if !bytes.Contains(w.Body.Bytes(), []byte(tc.code)) {
					t.Fatal(w.Body.String())
				}
				return
			}
			_, p, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			if err != nil || p["filename"] != "\u5831\u544a.txt" || w.Body.String() != "contents" || w.Header().Get("Content-Length") != "8" {
				t.Fatalf("bad download: %v %v %v", p, w.Header(), w.Body.String())
			}
		})
	}
}
func TestBrowserUploadLimits(t *testing.T) {
	for _, size := range []int64{-1, MaxUploadBytes + 1} {
		c := &transferClient{}
		r := transferRequest("POST", "http://example.com/api.cgi?method=nas.files.upload&parent=/home&name=large.bin", &largeReader{1})
		r.ContentLength = size
		r.Header.Set("Content-Type", "application/octet-stream")
		w := httptest.NewRecorder()
		transferServer(t, c).ServeHTTP(w, r)
		if c.read != 0 {
			t.Fatal("read rejected payload")
		}
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || out["success"] != false {
			t.Fatal(w.Body.String())
		}
	}
}

func TestBrowserDownloadForwardsTokenFromPOSTBodyNotURL(t *testing.T) {
	c := &transferClient{}
	r := transferRequest("POST", "http://example.com/api.cgi?method=nas.files.download&path=/home/a.txt", strings.NewReader("SynoToken=caller-token"))
	r.Header.Del("X-SYNO-TOKEN")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	transferServer(t, c).ServeHTTP(w, r)
	if w.Body.String() != "contents" || c.downloadToken != "caller-token" {
		t.Fatalf("POST transfer: %s token=%s", w.Body.String(), c.downloadToken)
	}
}

type transferTokenAuth struct{}

func (transferTokenAuth) Authenticate(r *http.Request) (api.Identity, error) {
	if r.Header.Get("X-SYNO-TOKEN") != "caller-token" {
		return api.Identity{}, api.ErrNoSession
	}
	return api.Identity{User: "fixtureuser1"}, nil
}
func TestPOSTDownloadTokenReachesCentralAuthentication(t *testing.T) {
	c := &transferClient{}
	s, err := api.NewServerWithPermissions(transferTokenAuth{}, []byte(`{"schema":1,"default":"administrators","methods":{}}`), NewService(c).Routes()...)
	if err != nil {
		t.Fatal(err)
	}
	r := transferRequest("POST", "http://example.com/api.cgi?method=nas.files.download&path=/home/a.txt", strings.NewReader("SynoToken=caller-token"))
	r.Header.Del("X-SYNO-TOKEN")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Body.String() != "contents" {
		t.Fatal(w.Body.String())
	}
}
