//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Run the compiled, shipped controller with a deliberately held health probe.
// The root-only fixture directory exists only inside the disposable gate container.
func TestCompiledSupervisorWaitsForActiveHealthProbe(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("disposable root container creates fixture INFO; controller still runs as nobody")
	}
	for _, finalStatus := range []int{204, 503} {
		t.Run(strconv.Itoa(finalStatus), func(t *testing.T) {
			control, dest, data := fixture(t, "normal")
			var hold atomic.Bool
			var responseStatus atomic.Int32
			responseStatus.Store(204)
			started, release := make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if hold.Load() {
					select {
					case started <- struct{}{}:
					default:
					}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				w.WriteHeader(int(responseStatus.Load()))
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			port, _ := strconv.Atoi(u.Port())
			raw, _ := os.ReadFile(filepath.Join(dest, "daemon/config.json"))
			var cfg config
			json.Unmarshal(raw, &cfg)
			cfg.Timeouts = deadlines{Start: 10, Stop: 2, Status: 2, Uninstall: 2}
			cfg.Network = &networkPolicy{Mode: "bridge", Ports: []int{port}, Readiness: &bridgeReadiness{Version: "1.0.0-0001", PlanSHA256: strings.Repeat("a", 64), Health: healthProbe{Path: "/health", Status: 204}}}
			raw, _ = json.Marshal(cfg)
			os.WriteFile(filepath.Join(dest, "daemon/config.json"), raw, 0644)
			info := filepath.Join("/var/packages", cfg.Package, "INFO")
			os.MkdirAll(filepath.Dir(info), 0755)
			defer os.RemoveAll(filepath.Dir(info))
			os.WriteFile(info, []byte("version=\"1.0.0-0001\"\n"), 0644)
			if rc, out := runControl(control, "acquire", dest, data); rc != 0 {
				t.Fatalf("acquire: %d %s", rc, out)
			}
			if rc, out := runControl(control, "start", dest, data); rc != 0 {
				t.Fatalf("start: %d %s", rc, out)
			}
			responseStatus.Store(int32(finalStatus))
			hold.Store(true)
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("periodic probe did not start")
			}
			raw, _ = os.ReadFile(filepath.Join(data, "daemon/daemon.pid"))
			var record record
			json.Unmarshal(raw, &record)
			conn, err := net.Dial("unix", filepath.Join(data, "daemon/ctl.sock"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			fmt.Fprintf(conn, "status %s\n", record.Digest)
			reader := bufio.NewReader(conn)
			conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			if line, err := reader.ReadString('\n'); err == nil {
				t.Fatalf("status answered %q while valid health probe was still running", line)
			}
			conn.SetReadDeadline(time.Now().Add(time.Second))
			once.Do(func() { close(release) })
			line, err := reader.ReadString('\n')
			want := "ok\n"
			if finalStatus != 204 {
				want = "unknown\n"
			}
			if err != nil || line != want {
				t.Fatalf("completed healthy probe: %q %v", line, err)
			}
		})
	}

}
