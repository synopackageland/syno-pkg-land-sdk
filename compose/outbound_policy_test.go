package compose

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDefaultRegistryClientsRejectLocalServices(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	srv.Listener = registryProbeListener{srv.Listener, &hits}
	srv.StartTLS()
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	if _, err := (RegistryResolver{}).Resolve(context.Background(), host+"/app:stable", "x86_64"); err == nil {
		t.Fatal("local registry accepted")
	}
	results := CheckRegistries(context.Background(), diagnosisProject(t, host+"/app"+diagnosisDigest), "postinst")
	if len(results) != 1 || hits.Load() != 0 {
		t.Fatal("registry diagnosis contacted local service", results, hits.Load())
	}
}

type registryProbeListener struct {
	net.Listener
	hits *atomic.Int32
}

func (l registryProbeListener) Accept() (net.Conn, error) {
	c, e := l.Listener.Accept()
	if e == nil {
		l.hits.Add(1)
	}
	return c, e
}
