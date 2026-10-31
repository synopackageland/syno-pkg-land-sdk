package delegate

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDefaultDownloadClientRejectsLocalServices(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer srv.Close()
	if response, err := downloadClient().Get(srv.URL); err == nil {
		response.Body.Close()
		t.Fatal("local service was contacted")
	}
	if hits.Load() != 0 {
		t.Fatal("request reached local service")
	}
}
