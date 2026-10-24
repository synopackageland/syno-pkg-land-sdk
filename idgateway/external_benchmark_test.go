package idgateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkEffectiveExternalAbsent(b *testing.B) {
	cfg := Config{Package: "App", Listen: Listen{HTTPS: 18443}}
	b.ReportAllocs()
	for b.Loop() {
		_ = EffectiveExternal(cfg, ExternalState{}, ExternalProjection{})
	}
}
func BenchmarkExternalAbsentReader(b *testing.B) {
	cfg := Config{Package: "App", StateDir: b.TempDir(), Listen: Listen{HTTPS: 18443}}
	reader := NewExternalReader(cfg)
	reader.paths = []string{b.TempDir() + "/absent.json"}
	reader.Read()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = reader.Read()
	}
}

// This route is entirely local and produces no transactions, cookies or
// upstream traffic. The callback is the production cached reader.
func BenchmarkExternalAbsentFront(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "baseline"
		if enabled {
			name = "reader"
		}
		b.Run(name, func(b *testing.B) {
			cfg := FrontConfig{Mode: ModeHeader, Upstream: "http://127.0.0.1:8080", Key: testKey, Package: "App", AuthorizeCGI: "/webman/3rdparty/App/sso/authorize.cgi", Accounts: fakeNames{1026: "alice"}, DSMPorts: DSMPorts{HTTPS: 5001}, Headers: HeaderNames{User: "Remote-User"}}
			if enabled {
				r := NewExternalReader(Config{Package: "App", StateDir: b.TempDir(), Listen: Listen{HTTPS: 18443}})
				r.paths = []string{b.TempDir() + "/absent.json"}
				r.Read()
				cfg.External = r.Read
			}
			front, err := NewFront(cfg)
			if err != nil {
				b.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "https://nas.example.com:18443/_sso/unknown", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				front.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
