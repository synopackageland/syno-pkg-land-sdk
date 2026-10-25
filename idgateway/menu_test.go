package idgateway

import (
	"os"
	"path/filepath"
	"testing"
)

func menuInputs() (Config, MenuSources) {
	cfg := Config{Package: "App", NativeIngress: true, Listen: Listen{HTTPS: 18443}, PublicOrigin: "https://local.example.com:18443"}
	launch := &LaunchMap{Schema: 1, Routes: []LaunchRoute{
		{Provider: ProviderCloudflare, DSM: "https://dsm.example.com", Apps: map[string]LaunchApp{
			"App":   {Origin: "https://app.example.com", Port: 18443, State: "active"},
			"Other": {Origin: "https://other.example.com", Port: 19443, State: "active"},
		}},
		{Provider: ProviderTailscale, DSM: "https://nas.example.ts.net:5001", Apps: map[string]LaunchApp{
			"App": {Origin: "https://nas.example.ts.net:18443", Port: 18443, State: "active"},
		}},
		{Provider: ProviderWorkers, DSM: "https://spl-dsm-1a2b.example.workers.dev", Apps: map[string]LaunchApp{
			"Other": {Origin: "https://spl-other-3c4d.example.workers.dev", Port: 19443, State: "active"},
		}},
	}}
	return cfg, MenuSources{State: ExternalState{Schema: 1}, Launch: launch}
}

func TestMenuDestinationProviders(t *testing.T) {
	cfg, src := menuInputs()
	for _, c := range []struct {
		host  string
		https bool
		want  MenuDecision
	}{
		{"dsm.example.com", true, MenuDecision{Kind: MenuRedirect, Origin: "https://app.example.com", Provider: ProviderCloudflare}},
		{"DSM.Example.com:443", true, MenuDecision{Kind: MenuRedirect, Origin: "https://app.example.com", Provider: ProviderCloudflare}},
		{"nas.example.ts.net:5001", true, MenuDecision{Kind: MenuRedirect, Origin: "https://nas.example.ts.net:18443", Provider: ProviderTailscale}},
		{"spl-dsm-1a2b.example.workers.dev", true, MenuDecision{Kind: MenuFallback, Provider: ProviderWorkers, Reason: MenuNotEnabled}},
		// LAN and unknown hosts keep the existing destination.
		{"192.0.2.10:5001", true, MenuDecision{}},
		{"nas.local:5001", true, MenuDecision{}},
		{"nas.example.ts.net:5002", true, MenuDecision{}},
		{"nas.example.ts.net", true, MenuDecision{}},
		{"dsm.example.com.", true, MenuDecision{}},
		{"", true, MenuDecision{}},
		{"example-id.relay.quickconnect.to", true, MenuDecision{Kind: MenuFallback, Provider: ProviderQuickConnect, Reason: MenuQuickConnect}},
		{"example-id.relay.QuickConnect.cn:443", true, MenuDecision{Kind: MenuFallback, Provider: ProviderQuickConnect, Reason: MenuQuickConnect}},
		{"192-0-2-10.example-id.direct.quickconnect.to:5001", true, MenuDecision{}},
		// Other hosts outside the local network check the DSM hostname and
		// App port before opening it, over HTTP too.
		{"dsm.example.com", false, MenuDecision{Kind: MenuRedirect, Origin: "https://dsm.example.com:18443", Provider: ProviderDirect}},
		{"sub.dsm.example.com", true, MenuDecision{Kind: MenuRedirect, Origin: "https://sub.dsm.example.com:18443", Provider: ProviderDirect}},
		{"quickconnect.to.example.com", true, MenuDecision{Kind: MenuRedirect, Origin: "https://quickconnect.to.example.com:18443", Provider: ProviderDirect}},
	} {
		if got := MenuDestination(cfg, src, c.host, c.https); got != c.want {
			t.Errorf("%q https=%v: %+v, want %+v", c.host, c.https, got, c.want)
		}
	}
}

func TestMenuDestinationPausedAndPortMismatch(t *testing.T) {
	cfg, src := menuInputs()
	src.Launch.Routes[0].Apps["App"] = LaunchApp{Origin: "https://app.example.com", Port: 18443, State: "paused"}
	src.Launch.Routes[1].Apps["App"] = LaunchApp{Origin: "https://nas.example.ts.net:18443", Port: 9999, State: "active"}
	for _, host := range []string{"dsm.example.com", "nas.example.ts.net:5001"} {
		if got := MenuDestination(cfg, src, host, true); got.Kind != MenuFallback || got.Reason != MenuPaused || got.Origin != "" {
			t.Fatal(host, got)
		}
	}
}

func TestMenuDestinationManualPairFirst(t *testing.T) {
	cfg, src := menuInputs()
	src.State = ExternalState{Schema: 1, Revision: 3, ManualOrigin: "https://manual.example.com", DSMOrigin: "https://dsm.example.com"}
	src.External = EffectiveExternal(cfg, src.State, src.Projection)
	if got := MenuDestination(cfg, src, "dsm.example.com", true); got.Origin != "https://manual.example.com" || got.Provider != ProviderManual {
		t.Fatal(got)
	}
	// A manual pair on its own DSM address, with no Connect.
	src.State.DSMOrigin = "https://proxy-dsm.example.com"
	src.Launch = nil
	src.External = EffectiveExternal(cfg, src.State, src.Projection)
	if got := MenuDestination(cfg, src, "proxy-dsm.example.com", true); got.Origin != "https://manual.example.com" {
		t.Fatal(got)
	}
	// A DSM origin without a manual App origin is no pair.
	src.State.ManualOrigin = ""
	src.External = EffectiveExternal(cfg, src.State, src.Projection)
	if got := MenuDestination(cfg, src, "proxy-dsm.example.com", true); got.Provider != ProviderDirect {
		t.Fatal(got)
	}
}

func TestMenuDestinationConnectNoneAndLegacyProjection(t *testing.T) {
	cfg, src := menuInputs()
	cfg.External = &ExternalConfig{Connect: "none"}
	if got := MenuDestination(cfg, src, "dsm.example.com", true); got != (MenuDecision{Kind: MenuRedirect, Origin: "https://dsm.example.com:18443", Provider: ProviderDirect}) {
		t.Fatal("connect none", got)
	}
	cfg.External = nil
	src.Launch = nil
	src.Projection = ExternalProjection{Schema: 1, DSM: ExternalProjectionDSM{Origin: "https://dsm.example.com"}, Apps: map[string]ExternalProjectionApp{
		"App": {Port: 18443, Rev: 2, Public: ExternalProjectionPublic{Origin: "https://app.example.com"}},
	}}
	if got := MenuDestination(cfg, src, "dsm.example.com", true); got.Origin != "https://app.example.com" || got.Provider != ProviderCloudflare {
		t.Fatal("legacy", got)
	}
	src.Projection.Apps = nil
	if got := MenuDestination(cfg, src, "dsm.example.com", true); got.Reason != MenuNotEnabled {
		t.Fatal("legacy without App", got)
	}
	// A launch map, even an empty one, replaces the projection pair.
	src.Launch = &LaunchMap{Schema: 1}
	src.Projection.Apps = map[string]ExternalProjectionApp{"App": {Port: 18443, Public: ExternalProjectionPublic{Origin: "https://app.example.com"}}}
	if got := MenuDestination(cfg, src, "dsm.example.com", true); got.Provider != ProviderDirect {
		t.Fatal("launch map replaces projection", got)
	}
}

func TestMenuDestinationSingleOrigin(t *testing.T) {
	cfg, src := menuInputs()
	cfg.External = &ExternalConfig{OriginChange: "confirm", SingleOrigin: true}
	// Without a canonical origin, a provider address cannot sign in.
	if got := MenuDestination(cfg, src, "nas.example.ts.net:5001", true); got.Kind != MenuFallback || got.Reason != MenuSingleLocal || got.Provider != ProviderTailscale {
		t.Fatal(got)
	}
	if got := MenuDestination(cfg, src, "192.0.2.10:5001", true); got != (MenuDecision{}) {
		t.Fatal("LAN", got)
	}
	src.State = ExternalState{Schema: 1, Revision: 1, ManualOrigin: "https://vault.example.com"}
	src.External = EffectiveExternal(cfg, src.State, src.Projection)
	for _, host := range []string{"192.0.2.10:5001", "dsm.example.com", "nas.example.ts.net:5001", "example-id.relay.quickconnect.to"} {
		if got := MenuDestination(cfg, src, host, true); got != (MenuDecision{Kind: MenuRedirect, Origin: "https://vault.example.com", Provider: ProviderCanonical}) {
			t.Fatal(host, got)
		}
	}
	// A tailnet address on the App port keeps its port.
	src.State.ManualOrigin = "https://vault.example.ts.net:18443"
	src.External = EffectiveExternal(cfg, src.State, src.Projection)
	if got := MenuDestination(cfg, src, "192.0.2.10:5001", true); got.Origin != "https://vault.example.ts.net:18443" || got.Kind != MenuRedirect {
		t.Fatal("ported", got)
	}
}

func TestMenuDestinationOutsideTheLocalNetwork(t *testing.T) {
	cfg, src := menuInputs()
	direct := func(origin string) MenuDecision {
		return MenuDecision{Kind: MenuRedirect, Origin: origin, Provider: ProviderDirect}
	}
	for host, want := range map[string]MenuDecision{
		// Private, loopback, link-local, shared and documentation addresses.
		"192.168.1.20:5001": {}, "10.0.0.2:5001": {}, "172.16.4.1": {}, "169.254.1.1:5001": {},
		"127.0.0.1:5001": {}, "100.101.102.103:5001": {}, "203.0.113.7:5001": {}, "0.0.0.0": {},
		// IPv6 literals cannot be a CSP source; every one keeps the redirect.
		"[fd00::1]:5001": {}, "[2606:4700::1]:5001": {},
		// Single labels, local and reserved names, tailnets.
		"nas:5001": {}, "localhost:5001": {}, "nas.local": {}, "nas.lan:5001": {}, "nas.home.arpa": {},
		"router.internal": {}, "nas.corp": {}, "nas.example": {}, "nas.test": {}, "nas.tailnet-name.ts.net:5001": {},
		"127.1": {}, "1.2.3": {}, "nas_1.example.com": {}, "nas.example.com.": {},
		"192-168-1-20.example-id.direct.quickconnect.to:5001": {}, "fd00--1.example-id.direct.quickconnect.to": {},
		// A domain, DDNS name or public address outside the local network.
		"nas.example.net:5001":    direct("https://nas.example.net:18443"),
		"NAS.Example.ORG":         direct("https://nas.example.org:18443"),
		"example-nas.synology.me": direct("https://example-nas.synology.me:18443"),
		"8.8.4.4:5001":            direct("https://8.8.4.4:18443"),
		"8-8-4-4.example-id.direct.quickconnect.to:5001": direct("https://8-8-4-4.example-id.direct.quickconnect.to:18443"),
	} {
		if got := MenuDestination(cfg, src, host, true); got != want {
			t.Errorf("%q: %+v, want %+v", host, got, want)
		}
	}
	// A trusted pair still wins over the direct address.
	if got := MenuDestination(cfg, src, "dsm.example.com", true); got.Provider != ProviderCloudflare {
		t.Fatal(got)
	}
	// The App port is the one the browser would open.
	cfg.Listen = Listen{HTTPS: 443}
	if got := MenuDestination(cfg, src, "nas.example.net:5001", true); got.Origin != "https://nas.example.net:443" {
		t.Fatal(got)
	}
}

func TestMenuDestinationWithoutNativeIngress(t *testing.T) {
	cfg, src := menuInputs()
	cfg.NativeIngress = false
	for _, host := range []string{"dsm.example.com", "example-id.relay.quickconnect.to"} {
		if got := MenuDestination(cfg, src, host, true); got != (MenuDecision{}) {
			t.Fatal(host, got)
		}
	}
}

func TestLaunchMapValidation(t *testing.T) {
	for name, edit := range map[string]func(*LaunchMap){
		"schema":      func(m *LaunchMap) { m.Schema = 2 },
		"provider":    func(m *LaunchMap) { m.Routes[0].Provider = "manual" },
		"public port": func(m *LaunchMap) { m.Routes[0].DSM = "https://dsm.example.com:5001" },
		"app public port": func(m *LaunchMap) {
			m.Routes[0].Apps["App"] = LaunchApp{Origin: "https://app.example.com:8443", Port: 18443, State: "active"}
		},
		"ip":   func(m *LaunchMap) { m.Routes[1].DSM = "https://192.0.2.10:5001" },
		"path": func(m *LaunchMap) { m.Routes[1].DSM = "https://nas.example.ts.net:5001/" },
		"http": func(m *LaunchMap) {
			m.Routes[0].Apps["App"] = LaunchApp{Origin: "http://app.example.com", Port: 18443, State: "active"}
		},
		"state": func(m *LaunchMap) {
			m.Routes[0].Apps["App"] = LaunchApp{Origin: "https://app.example.com", Port: 18443, State: "removed"}
		},
		"port": func(m *LaunchMap) {
			m.Routes[0].Apps["App"] = LaunchApp{Origin: "https://app.example.com", Port: 0, State: "active"}
		},
		"name": func(m *LaunchMap) {
			m.Routes[0].Apps["../App"] = LaunchApp{Origin: "https://app.example.com", Port: 1, State: "active"}
		},
		"routes": func(m *LaunchMap) {
			for len(m.Routes) <= 8 {
				m.Routes = append(m.Routes, m.Routes[0])
			}
		},
	} {
		_, src := menuInputs()
		edit(src.Launch)
		if src.Launch.valid() {
			t.Error("accepted", name)
		}
	}
	_, src := menuInputs()
	if !src.Launch.valid() {
		t.Fatal("rejected the fixture")
	}
}

func TestLoadMenuSources(t *testing.T) {
	cfg, src := menuInputs()
	cfg.StateDir = t.TempDir()
	dir := t.TempDir()
	projection := filepath.Join(dir, "external.json")
	launch := filepath.Join(dir, "launch.json")
	missing := filepath.Join(dir, "missing", "launch.json")
	writeExternalJSON(t, cfg.ExternalStatePath(), ExternalState{Schema: 1, Revision: 4, ManualOrigin: "https://manual.example.com", DSMOrigin: "https://proxy-dsm.example.com"}, 0600)
	writeExternalJSON(t, projection, ExternalProjection{Schema: 1, Apps: map[string]ExternalProjectionApp{}}, 0644)
	writeExternalJSON(t, launch, src.Launch, 0644)
	got := loadMenuSources(cfg, []string{projection}, []string{missing, launch})
	if got.Launch == nil || len(got.Launch.Routes) != 3 || got.State.Revision != 4 || got.External.Manual != "https://manual.example.com" {
		t.Fatalf("%+v", got)
	}
	if d := MenuDestination(cfg, got, "dsm.example.com", true); d.Origin != "https://app.example.com" {
		t.Fatal(d)
	}
	// Unsafe or malformed launch maps are absent, never a stale fallback.
	for name, prepare := range map[string]func(){
		"group writable": func() { os.Chmod(launch, 0664) },
		"private mode":   func() { os.Chmod(launch, 0600) },
		"unknown field": func() {
			os.WriteFile(launch, []byte(`{"schema":1,"routes":[],"extra":1}`), 0644)
		},
		"symlink": func() {
			os.Remove(launch)
			other := filepath.Join(t.TempDir(), "other.json")
			writeExternalJSON(t, other, src.Launch, 0644)
			os.Symlink(other, launch)
		},
	} {
		os.Remove(launch)
		writeExternalJSON(t, launch, src.Launch, 0644)
		prepare()
		fallback := filepath.Join(t.TempDir(), "launch.json")
		writeExternalJSON(t, fallback, src.Launch, 0644)
		if got := loadMenuSources(cfg, []string{projection}, []string{launch, fallback}); got.Launch != nil {
			t.Error(name, "was read")
		}
	}
	cfg.External = &ExternalConfig{Connect: "none"}
	os.Remove(launch)
	writeExternalJSON(t, launch, src.Launch, 0644)
	if got := loadMenuSources(cfg, []string{projection}, []string{launch}); got.Launch != nil {
		t.Fatal("connect none read the launch map")
	}
}
