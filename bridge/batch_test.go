//go:build unix

package bridge

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type countedDialer struct {
	dialer Dialer
	calls  int
}

func (d *countedDialer) Dial(ctx context.Context) (net.Conn, error) {
	d.calls++
	return d.dialer.Dial(ctx)
}

func TestBatchAdaptersUseOneAppAndIsolateEachResult(t *testing.T) {
	for _, a := range goldenApps {
		t.Run(a.name, func(t *testing.T) {
			path, args, env := a.cmd(t)
			d := &countedDialer{dialer: &Exec{Path: path, Args: args, Dir: root(t), Env: env}}
			s := &Session{Dial: d, Methods: goldenMethods, Services: map[string]Service{"demo.service": func(_ context.Context, c *Call, _ json.RawMessage) (any, error) { return c.Caller.User(), nil }}}
			policy := []byte(`{"schema":1,"default":"administrators","methods":{"demo.echo":"authenticated","demo.fail":"authenticated","demo.svc":"authenticated","demo.svc_public":"public"}}`)
			server, err := api.NewServerWithPermissions(goldenAuth{"alice"}, policy, Routes(s)...)
			if err != nil {
				t.Fatal(err)
			}
			body := `{"v":1,"calls":[{"method":"demo.echo","params":{"n":1}},{"method":"demo.fail"},{"method":"demo.admin"},{"method":"demo.svc","params":{"op":"demo.service"}},{"method":"demo.svc_public","params":{"op":"demo.service"}},{"method":"demo.echo","params":{"n":2}},{"method":"demo.echo"}]}`
			r := httptest.NewRequest(http.MethodPost, "http://nas/cgi/api.cgi", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "http://nas")
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			var out struct {
				Success bool `json:"success"`
				Data    struct {
					Results []struct {
						Success bool            `json:"success"`
						Data    json.RawMessage `json:"data"`
						Error   *struct {
							Code string `json:"code"`
						} `json:"error"`
					} `json:"results"`
				} `json:"data"`
			}
			if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.Success || len(out.Data.Results) != 7 {
				t.Fatalf("%s", w.Body.String())
			}
			results := out.Data.Results
			if !results[0].Success || results[1].Error == nil || results[1].Error.Code != "conflict" || results[2].Error == nil || results[2].Error.Code != "admin_required" || string(results[3].Data) != `{"result":"alice"}` || string(results[4].Data) != `{"result":""}` || !results[5].Success || !results[6].Success || !strings.Contains(string(results[6].Data), `"params":{}`) || d.calls != 1 {
				t.Fatalf("dials=%d reply=%s", d.calls, w.Body.String())
			}
		})
	}
}

// Opt-in local process benchmark; no NAS or DSM authentication is simulated.
// It measures the real adapters with the golden authenticator, not api.cgi.
func TestMeasureStartupBatchProcesses(t *testing.T) {
	if os.Getenv("SYNOPKGLAND_MEASURE_BATCH") == "" {
		t.Skip("opt-in adapter process measurement")
	}
	for _, a := range goldenApps {
		if a.name == "rust" {
			continue
		}
		t.Run(a.name, func(t *testing.T) {
			path, args, env := a.cmd(t)
			d := &countedDialer{dialer: &Exec{Path: path, Args: args, Dir: root(t), Env: env}}
			server := goldenServer(t, "alice", d)
			one := `{"method":"demo.echo","params":{"n":1}}`
			batch := `{"v":1,"calls":[{"method":"demo.echo","params":{"n":1}},{"method":"demo.echo","params":{"n":2}},{"method":"demo.echo","params":{"n":3}}]}`
			serve(t, server, one, nil)
			var singleMS, batchMS []float64
			for range 7 {
				before := d.calls
				start := time.Now()
				for range 3 {
					serve(t, server, one, nil)
				}
				singleMS = append(singleMS, float64(time.Since(start).Microseconds())/1000)
				if d.calls-before != 3 {
					t.Fatal("single process count")
				}
				before = d.calls
				start = time.Now()
				reply := serve(t, server, batch, nil)
				batchMS = append(batchMS, float64(time.Since(start).Microseconds())/1000)
				if d.calls-before != 1 || !strings.Contains(reply, `"results"`) {
					t.Fatal("batch process count", reply)
				}
			}
			sort.Float64s(singleMS)
			sort.Float64s(batchMS)
			t.Logf("three reads: single median %.3f ms (3 processes), batch median %.3f ms (1 process); 7 warm samples", singleMS[3], batchMS[3])
		})
	}
}
