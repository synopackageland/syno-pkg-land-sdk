package delegate

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type batchAuthn struct{}

func (batchAuthn) Authenticate(*http.Request) (api.Identity, error) {
	return api.Identity{User: "alice", Admin: true}, nil
}

func TestHTTPBatchSharesOneExecutorConnectionAndSingleCallsStayFresh(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "executor.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var connections, verified, steps atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for connection := 0; connection < 2; connection++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			decode, encode := json.NewDecoder(conn), json.NewEncoder(conn)
			var first Request
			if decode.Decode(&first) != nil {
				conn.Close()
				return
			}
			verified.Add(1)
			if first.Auth["dsm_cookie"] != "sid" {
				conn.Close()
				return
			}
			if first.Op == "root.batch" {
				encode.Encode(map[string]any{"ok": true, "result": map[string]int{"v": 1}})
				for {
					var step Request
					err := decode.Decode(&step)
					if err == io.EOF {
						break
					}
					if err != nil || step.Op != "root.run" || len(step.Auth) != 0 {
						conn.Close()
						return
					}
					n := steps.Add(1)
					if n == 2 {
						encode.Encode(map[string]any{"ok": false, "code": "POLICY_DENIED"})
					} else {
						encode.Encode(map[string]any{"ok": true, "result": map[string]bool{"ran": true}})
					}
				}
			} else if first.Op == "root.run" {
				steps.Add(1)
				encode.Encode(map[string]any{"ok": true, "result": map[string]bool{"ran": true}})
			}
			conn.Close()
		}
	}()
	client := &Client{Socket: socket}
	handler := api.Handle("demo.read", func(ctx context.Context, _ api.Caller, _ json.RawMessage) (any, error) {
		var out json.RawMessage
		err := client.Call(ctx, "root.run", map[string]string{"dsm_cookie": "sid", "dsm_synotoken": "token", "remote_addr": "192.0.2.1"}, map[string]string{"name": "read"}, &out)
		return out, err
	})
	server, err := api.NewServerWithPermissions(batchAuthn{}, []byte(`{"schema":1,"default":"administrators","methods":{}}`), handler)
	if err != nil {
		t.Fatal(err)
	}
	request := func(body string) string {
		r := httptest.NewRequest("POST", "http://nas/api.cgi", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://nas")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w.Body.String()
	}
	batch := request(`{"v":1,"calls":[{"method":"demo.read"},{"method":"demo.read"},{"method":"demo.read"}]}`)
	if connections.Load() != 1 || verified.Load() != 1 || steps.Load() != 3 || strings.Count(batch, `"ran":true`) != 2 || !strings.Contains(batch, `"code":"POLICY_DENIED"`) {
		t.Fatalf("connections=%d verified=%d steps=%d reply=%s", connections.Load(), verified.Load(), steps.Load(), batch)
	}
	single := request(`{"method":"demo.read"}`)
	<-done
	if connections.Load() != 2 || verified.Load() != 2 || steps.Load() != 4 || !strings.Contains(single, `"ran":true`) {
		t.Fatalf("fresh single: %s %d %d", single, connections.Load(), verified.Load())
	}
}
