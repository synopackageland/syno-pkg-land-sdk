package idgateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
)

type shareAuth struct{ denied bool }

func (a shareAuth) Authenticate(*http.Request) (api.Identity, error) {
	if a.denied {
		return api.Identity{}, errors.New("session required")
	}
	return api.Identity{User: "admin", Admin: true}, nil
}

func TestStoppedComposeEntryExplainsShareAndRetriesWithoutStarting(t *testing.T) {
	denied := true
	calls := 0
	check := func() error {
		calls++
		if denied {
			return &sharedfolder.PermissionError{Account: "ExampleApp", Name: "Documents <private>", Need: sharedfolder.Write}
		}
		return nil
	}
	h := entryHandlerWithShares(Config{ContainerApp: true, Listen: Listen{HTTP: 28000}}, func() error { return nil }, check, shareAuth{})
	request := func(target, lang string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", target, nil)
		r.Header.Set("Accept-Language", lang)
		h.ServeHTTP(w, r)
		return w
	}
	w := request("https://nas.example/open.cgi", "en")
	for _, part := range []string{`<dialog open`, "System internal user", "ExampleApp", "Read/Write", "Retry", "Documents &lt;private&gt;"} {
		if !strings.Contains(w.Body.String(), part) {
			t.Errorf("missing %q: %s", part, w.Body)
		}
	}
	if strings.Contains(w.Body.String(), "aria-modal") || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header(), w.Body)
	}
	w = request("https://nas.example/open.cgi?sharedfolder=retry", "zh-TW")
	if !strings.Contains(w.Body.String(), "系統內部使用者") {
		t.Fatal(w.Body)
	}
	denied = false
	w = request("https://nas.example/open.cgi?sharedfolder=retry", "en")
	if !strings.Contains(w.Body.String(), "Package Center") || w.Header().Get("Location") != "" || calls != 3 {
		t.Fatal(calls, w.Header(), w.Body)
	}
	// A later launch returns to the ordinary entry; no persisted permission flag.
	w = request("https://nas.example/open.cgi", "en")
	if strings.Contains(w.Body.String(), "<dialog") {
		t.Fatal(w.Body)
	}
}

func TestShareEntryNeverRevealsNamesWithoutDSMAdmission(t *testing.T) {
	check := func() error {
		return &sharedfolder.PermissionError{Account: "PrivateApp", Name: "PrivateShare", Need: sharedfolder.Write}
	}
	h := entryHandlerWithShares(Config{ContainerApp: true}, func() error { return nil }, check, shareAuth{denied: true})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "https://nas.example/open.cgi", nil)
	r.Header.Set("Accept-Language", "zh-TW")
	h.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "PrivateShare") || strings.Contains(w.Body.String(), "PrivateApp") || w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	if w.Header().Get("Content-Language") != "zh-TW" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
}

func TestSharePromptCatalogsMatchTheFrontendSteps(t *testing.T) {
	for _, locale := range denialLocales {
		raw, _ := denialCatalogs.ReadFile("i18n/" + locale + ".json")
		var server, frontend map[string]string
		if err := json.Unmarshal(raw, &server); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join("../ts/i18n/sharedfolder", locale+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &frontend); err != nil {
			t.Fatal(err)
		}
		if server["share_steps"] != frontend["sharedfolder.steps"] {
			t.Errorf("%s: server/frontend instructions differ", locale)
		}
		for _, key := range []string{"share_title", "share_steps", "share_read", "share_write", "share_retry", "share_dismiss", "share_ready", "share_next_start", "share_unavailable"} {
			text := server[key]
			if text == "" || strings.HasSuffix(text, ".") || strings.HasSuffix(text, "。") {
				t.Errorf("%s: invalid %s", locale, key)
			}
		}
	}
}
