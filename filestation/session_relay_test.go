package filestation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type unreadUpload struct{ reads int }

func (r *unreadUpload) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }

func TestLegacyClientNeverReplaysBrowserSession(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		io.WriteString(w, `{"success":true,"data":{"username":"alice","files":[],"blSkip":false}}`)
	}))
	defer server.Close()
	c, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	r := sessionRequest()
	source := &unreadUpload{}
	ctx := context.Background()
	checks := map[string]func() error{
		"verify":     func() error { return c.Verify(ctx, r, "alice") },
		"shares":     func() error { _, e := c.Discover(ctx, r, 0); return e },
		"list":       func() error { _, e := c.List(ctx, r, "/home", ListOptions{}); return e },
		"info":       func() error { _, e := c.Stat(ctx, r, "/home/a"); return e },
		"create":     func() error { return c.Create(ctx, r, "/home", "new") },
		"permission": func() error { return c.CheckPermission(ctx, r, "/home") },
		"download": func() error {
			d, e := c.DownloadStream(ctx, r, "/home/a")
			if d.Body != nil {
				d.Body.Close()
			}
			return e
		},
		"read":   func() error { _, e := c.Read(ctx, r, "/home/a"); return e },
		"upload": func() error { return c.UploadStream(ctx, r, "/home", "a", source, 1) },
	}
	for name, run := range checks {
		t.Run(name, func(t *testing.T) {
			var refused *api.Error
			if e := run(); !errors.As(e, &refused) || refused.Code != "unsupported" {
				t.Fatalf("want unsupported, got %v", e)
			}
		})
	}
	if calls != 0 || source.reads != 0 {
		t.Fatalf("session replay: HTTP=%d upload reads=%d", calls, source.reads)
	}
}

func TestLocalStatusDescribesBrowserTransportWithoutContactingDSM(t *testing.T) {
	out := invoke(t, Local(t.TempDir()), "alice", "nas.session.status", `{}`)
	if out["success"] != true {
		t.Fatalf("status: %v", out)
	}
	data, _ := out["data"].(map[string]any)
	if data["mode"] != "browser-session" {
		t.Fatalf("unsafe transport advertised: %v", data)
	}
	if strings.Contains(data["mode"].(string), "loopback") {
		t.Fatal(data)
	}
}
