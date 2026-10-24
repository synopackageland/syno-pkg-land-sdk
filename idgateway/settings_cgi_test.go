package idgateway

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func settingsFixture(t *testing.T) *SettingsCGI {
	t.Helper()
	return &SettingsCGI{
		Config:     Config{Package: "Fixture", StateDir: filepath.Join(t.TempDir(), "sso"), Listen: Listen{HTTPS: 18443}},
		Verifier:   &fakeDSM{user: "operator", token: "tok", addr: "192.0.2.7", admin: true},
		Membership: &independentRole{admin: true},
		Projection: func() ExternalProjection {
			return ExternalProjection{Schema: 1, DSM: ExternalProjectionDSM{Origin: "https://dsm.example.com"}, Apps: map[string]ExternalProjectionApp{"Fixture": {Port: 18443, Rev: 7, Public: ExternalProjectionPublic{Origin: "https://connect.example.com"}}}}
		},
	}
}

func settingsPost(h *SettingsCGI, values url.Values, origin, cookie, locale string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "https://nas.example:5001/webman/3rdparty/Fixture/sso/settings.cgi", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", origin)
	r.Header.Set("Cookie", cookie)
	r.Header.Set("Accept-Language", locale)
	r.RemoteAddr = "192.0.2.7:5555"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func settingsSave(revision uint64, manual, dsm string) url.Values {
	return url.Values{"SynoToken": {"tok"}, "action": {"save"}, "revision": {strconv.FormatUint(revision, 10)}, "manual_origin": {manual}, "dsm_origin": {dsm}}
}

func TestSettingsGETOnlyBootstrapsWithoutReadingProtectedData(t *testing.T) {
	h := settingsFixture(t)
	h.Projection = func() ExternalProjection { t.Fatal("bootstrap read projection"); return ExternalProjection{} }
	h.Config.PublicOrigin = "https://protected.example.com"
	h.Config.StateDir = filepath.Join(t.TempDir(), "not-created")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example:5001/settings.cgi?manual_origin=secret", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "enable_syno_token=yes") || strings.Contains(w.Body.String(), "protected.example.com") || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("bootstrap: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(h.Config.StateDir); !os.IsNotExist(err) {
		t.Fatal("bootstrap created state")
	}
	if w.Header().Get("Referrer-Policy") != "same-origin" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'nonce-") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
}

func TestSettingsRequiresSessionTokenSameOriginAndNativeAdministrator(t *testing.T) {
	for _, tc := range []struct {
		name, token, origin, cookie string
		admin                       bool
	}{
		{"missing token", "", "https://nas.example:5001", "id=s1", true},
		{"wrong token", "wrong", "https://nas.example:5001", "id=s1", true},
		{"missing cookie", "tok", "https://nas.example:5001", "", true},
		{"foreign origin", "tok", "https://other.example.com", "id=s1", true},
		{"null origin", "tok", "null", "id=s1", true},
		{"missing origin", "tok", "", "id=s1", true},
		{"non administrator", "tok", "https://nas.example:5001", "id=s1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := settingsFixture(t)
			h.Membership = &independentRole{admin: tc.admin}
			h.Projection = func() ExternalProjection { t.Fatal("unauthorized read"); return ExternalProjection{} }
			w := settingsPost(h, url.Values{"SynoToken": {tc.token}}, tc.origin, tc.cookie, "en")
			if w.Code != 403 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if _, err := os.Stat(h.Config.StateDir); !os.IsNotExist(err) {
				t.Fatal("unauthorized mutation")
			}
		})
	}
}

func TestSettingsShowsIndependentSourcesAndWritesDurableRevisions(t *testing.T) {
	h := settingsFixture(t)
	for _, manual := range []string{"https://MANUAL.example.com:443", "https://manual.example.com", "", "https://manual.example.com"} {
		state, _ := LoadExternalState(h.Config)
		w := settingsPost(h, settingsSave(state.Revision, manual, "https://dsm.example.com"), "https://nas.example:5001", "id=s1", "zh-TW")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		next, err := LoadExternalState(h.Config)
		if err != nil || next.Revision != state.Revision+1 {
			t.Fatalf("revision: %+v %v", next, err)
		}
		if manual != "" && next.ManualOrigin != "https://manual.example.com" {
			t.Fatal(next)
		}
		if !strings.Contains(w.Body.String(), "https://connect.example.com") || !strings.Contains(w.Body.String(), "https://dsm.example.com") || w.Header().Get("Content-Language") != "zh-TW" {
			t.Fatal(w.Body.String())
		}
		st, err := os.Stat(h.Config.ExternalStatePath())
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatal("state not private", err)
		}
	}
	w := settingsPost(h, settingsSave(0, "https://stale.example.com", ""), "https://nas.example:5001", "id=s1", "en")
	if w.Code != 409 {
		t.Fatalf("stale save: %d", w.Code)
	}
	state, _ := LoadExternalState(h.Config)
	if state.Revision != 4 || state.ManualOrigin != "https://manual.example.com" {
		t.Fatal(state)
	}
}

func TestSettingsRejectsInvalidOriginsAndRequiresChangeConfirmation(t *testing.T) {
	for _, origin := range []string{"https://127.0.0.1", "https://app.example.com/path", "http://app.example.com", "https://app.example.com:8443", "https://dsm.example.com"} {
		h := settingsFixture(t)
		w := settingsPost(h, settingsSave(0, origin, "https://dsm.example.com"), "https://nas.example:5001", "id=s1", "en")
		if w.Code != 400 {
			t.Fatalf("accepted %q: %d", origin, w.Code)
		}
	}
	h := settingsFixture(t)
	h.Config.External = &ExternalConfig{OriginChange: "confirm"}
	v := settingsSave(0, "https://manual.example.com", "https://dsm.example.com")
	if w := settingsPost(h, v, "https://nas.example:5001", "id=s1", "en"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	v.Set("confirm", "yes")
	if w := settingsPost(h, v, "https://nas.example:5001", "id=s1", "en"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	// A DSM edit cannot collide with the projected App origin either.
	if w := settingsPost(h, settingsSave(1, "", "https://connect.example.com"), "https://nas.example:5001", "id=s1", "en"); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestSettingsConcurrentSavesDoNotLoseRevisions(t *testing.T) {
	h := settingsFixture(t)
	// The authenticator mock records its input; each CGI process has its own.
	a, b := *h, *h
	b.Verifier = &fakeDSM{user: "operator", token: "tok", addr: "192.0.2.7", admin: true}
	b.Membership = &independentRole{admin: true}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, handler := range []*SettingsCGI{&a, &b} {
		wg.Add(1)
		go func(h *SettingsCGI) {
			defer wg.Done()
			statuses <- settingsPost(h, settingsSave(0, "https://app.example.com", ""), "https://nas.example:5001", "id=s1", "en").Code
		}(handler)
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
	state, err := LoadExternalState(h.Config)
	if err != nil || state.Revision != 1 {
		t.Fatal(state, err)
	}
}

func TestSettingsRefusesUnsafeFilesAndRevisionExhaustion(t *testing.T) {
	for _, kind := range []string{"state symlink", "lock symlink", "exhausted revision", "corrupt state"} {
		t.Run(kind, func(t *testing.T) {
			h := settingsFixture(t)
			os.Mkdir(h.Config.StateDir, 0700)
			outside := filepath.Join(t.TempDir(), "outside")
			os.WriteFile(outside, []byte("unchanged"), 0600)
			revision := uint64(0)
			switch kind {
			case "state symlink":
				os.Symlink(outside, h.Config.ExternalStatePath())
			case "lock symlink":
				os.Symlink(outside, filepath.Join(h.Config.StateDir, "external.lock"))
			case "exhausted revision":
				revision = maxExternalRevision
				raw, _ := json.Marshal(ExternalState{Schema: 1, Revision: revision})
				os.WriteFile(h.Config.ExternalStatePath(), raw, 0600)
			case "corrupt state":
				os.WriteFile(h.Config.ExternalStatePath(), []byte("{"), 0600)
			}
			w := settingsPost(h, settingsSave(revision, "https://app.example.com", ""), "https://nas.example:5001", "id=s1", "en")
			if w.Code < 400 {
				t.Fatal("unsafe save accepted", kind, w.Code)
			}
			if b, _ := os.ReadFile(outside); string(b) != "unchanged" {
				t.Fatal("external file changed")
			}
		})
	}
}

func TestSettingsEscapesProtectedTextAndIgnoresForwardedHeaders(t *testing.T) {
	h := settingsFixture(t)
	h.Config.Passthrough = []string{`/<script>alert(1)</script>`}
	r := httptest.NewRequest("POST", "https://nas.example:5001/settings.cgi", strings.NewReader("SynoToken=tok"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://nas.example:5001")
	r.Header.Set("Cookie", "id=s1")
	r.Header.Set("X-Forwarded-Host", "attacker.example.com")
	r.RemoteAddr = "192.0.2.7:5555"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>") || strings.Contains(w.Body.String(), "attacker.example.com") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal(w.Code, w.Body)
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self'") {
		t.Fatal(w.Header())
	}
}

func TestSettingsUsesTheSharedNativePublicPort(t *testing.T) {
	h := settingsFixture(t)
	h.Config.Listen = Listen{HTTP: 18443}
	h.Config.NativeIngress = true
	w := settingsPost(h, url.Values{"SynoToken": {"tok"}}, "https://nas.example:5001", "id=s1", "en")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://nas.example:18443") {
		t.Fatal(w.Code, w.Body)
	}
	w = settingsPost(h, settingsSave(0, "", "https://connect.example.com"), "https://nas.example:5001", "id=s1", "en")
	if w.Code != 400 {
		t.Fatal("allowed App and DSM to share an origin", w.Code)
	}
}

func TestSettingsLocaleCatalogsCompleteAndWithoutTrailingPeriods(t *testing.T) {
	for _, locale := range denialLocales {
		raw, _ := denialCatalogs.ReadFile("i18n/" + locale + ".json")
		var messages map[string]string
		if err := json.Unmarshal(raw, &messages); err != nil {
			t.Fatal(err)
		}
		count := 0
		for key, value := range messages {
			if strings.HasPrefix(key, "external_") {
				count++
				if value == "" || strings.HasSuffix(value, ".") || strings.HasSuffix(value, "。") {
					t.Fatal(locale, key)
				}
			}
		}
		if count != 21 {
			t.Fatal(locale, count)
		}
		h := settingsFixture(t)
		w := settingsPost(h, url.Values{"SynoToken": {"tok"}}, "https://nas.example:5001", "id=s1", locale)
		if w.Code != 200 || w.Header().Get("Content-Language") != locale || strings.Contains(w.Body.String(), "<no value>") {
			t.Fatal(locale, w.Code, w.Body)
		}
	}
}
