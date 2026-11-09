package delegate

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// The deadline is only a test watchdog: production must decide from the
// mailbox and failed dial, without consuming any startup/work allowance.
func TestStartingStatusDoesNotWaitForSocket(t *testing.T) {
	for _, batched := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "authenticated batch"}[batched], func(t *testing.T) {
			c := &Client{Socket: filepath.Join(t.TempDir(), "absent"), Mailbox: mailbox(t, "start", 0)}
			route := api.Handle("probe.status", func(ctx context.Context, _ api.Caller, _ json.RawMessage) (any, error) {
				return nil, c.Call(ctx, "status", map[string]string{"dsm_cookie": "sid"}, nil, nil)
			})
			routes := append(ExecutorRoutes(c, t.TempDir()), route)
			server, err := api.NewServerWithPermissions(batchAuthn{}, []byte(`{"schema":1,"default":"administrators","methods":{}}`), routes...)
			if err != nil {
				t.Fatal(err)
			}
			body := `{"method":"executor.status"}`
			if batched {
				body = `{"v":1,"calls":[{"method":"probe.status"},{"method":"probe.status"}]}`
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			r := httptest.NewRequest(http.MethodPost, "http://nas/api.cgi", strings.NewReader(body)).WithContext(ctx)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "http://nas")
			w := httptest.NewRecorder()
			begin := time.Now()
			server.ServeHTTP(w, r)
			t.Logf("status response after %v", time.Since(begin))
			if !strings.Contains(w.Body.String(), `"code":"APP_STARTING"`) || time.Since(begin) >= 500*time.Millisecond {
				t.Fatalf("status waited for startup (%v): %s", time.Since(begin), w.Body.String())
			}
		})
	}
	t.Run("capability", func(t *testing.T) {
		c := &Client{Socket: filepath.Join(t.TempDir(), "absent"), Mailbox: mailbox(t, "start", 0)}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		begin := time.Now()
		got := (&Service{Client: c}).Status(ctx, nil)
		t.Logf("capability response after %v", time.Since(begin))
		if got.State != "blocked" || got.Reason != CodeAppStarting || time.Since(begin) >= 500*time.Millisecond {
			t.Fatalf("capability waited for startup (%v): %+v", time.Since(begin), got)
		}
	})
}

func TestStartingStatusDoesNotPoisonWorkInTheSameBatch(t *testing.T) {
	c := &Client{Socket: filepath.Join(t.TempDir(), "executor.sock"), Mailbox: mailbox(t, "start", 0)}
	auth := map[string]string{"dsm_cookie": "sid"}
	status := api.Handle("probe.status", func(ctx context.Context, _ api.Caller, _ json.RawMessage) (any, error) {
		return nil, c.Call(ctx, "status", auth, nil, nil)
	})
	work := api.Handle("probe.work", func(ctx context.Context, _ api.Caller, _ json.RawMessage) (any, error) {
		// Actual work must still wait for a listener that appears after its dial.
		timer := time.AfterFunc(200*time.Millisecond, func() {
			l, err := net.Listen("unix", c.Socket)
			if err != nil {
				t.Error(err)
				return
			}
			defer l.Close()
			conn, err := l.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			decode, encode := json.NewDecoder(conn), json.NewEncoder(conn)
			for _, op := range []string{"root.batch", "root.run"} {
				var request Request
				if err := decode.Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request.Op != op {
					t.Errorf("got %s, want %s", request.Op, op)
					return
				}
				encode.Encode(map[string]any{"ok": true, "result": map[string]bool{"ran": true}})
			}
		})
		defer timer.Stop()
		var out json.RawMessage
		err := c.Call(ctx, "root.run", auth, nil, &out)
		return out, err
	})
	server, err := api.NewServerWithPermissions(batchAuthn{}, []byte(`{"schema":1,"default":"administrators","methods":{}}`), status, work)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := httptest.NewRequest(http.MethodPost, "http://nas/api.cgi", strings.NewReader(`{"v":1,"calls":[{"method":"probe.status"},{"method":"probe.work"}]}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://nas")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `"code":"APP_STARTING"`) || !strings.Contains(w.Body.String(), `"ran":true`) {
		t.Fatal(w.Body.String())
	}
}
