package bridge

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// The locales of the SDK's UI catalogs (ts/src/i18n.ts uiLocales).
var catalogLocales = []string{"en", "zh-TW", "zh-CN", "ja", "ko", "de", "fr", "es", "it"}

type catalogEntry struct {
	Code      string `json:"code"`
	Kind      string `json:"kind"`
	Retryable bool   `json:"retryable"`
}

func bridgeCatalog(t *testing.T) (codes []catalogEntry, texts []map[string]any) {
	t.Helper()
	raw, err := os.ReadFile("../spec/bridge/v1/errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Codes []json.RawMessage }
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Codes {
		var e catalogEntry
		var m map[string]any
		json.Unmarshal(c, &e)
		json.Unmarshal(c, &m)
		codes, texts = append(codes, e), append(texts, m)
	}
	return codes, texts
}

// Every code the runtime adds to the api codes is in spec/bridge/v1/errors.json,
// in every UI locale, and its message is the catalog's en text.
func TestRuntimeCodesAreInTheBridgeCatalog(t *testing.T) {
	codes, texts := bridgeCatalog(t)
	runtime := []*struct{ Code, Message string }{{AppUnavailable().Code, AppUnavailable().Message}}
	for _, want := range runtime {
		i := slices.IndexFunc(codes, func(e catalogEntry) bool { return e.Code == want.Code })
		if i < 0 {
			t.Fatalf("%s is not in the catalog", want.Code)
		}
		if codes[i].Kind != "refusal" || !reservedCodes[want.Code] {
			t.Errorf("%s: kind %q, reserved %v", want.Code, codes[i].Kind, reservedCodes[want.Code])
		}
		if texts[i]["en"] != want.Message {
			t.Errorf("%s: message %q, catalog en %q", want.Code, want.Message, texts[i]["en"])
		}
		for _, locale := range catalogLocales {
			if s, _ := texts[i][locale].(string); s == "" {
				t.Errorf("%s: no %s text", want.Code, locale)
			}
		}
	}
	services := []*api.Error{limitExceeded(), readFailed(), identityUnavailable(), api.CredentialNotReady()}
	for _, want := range services {
		i := slices.IndexFunc(codes, func(e catalogEntry) bool { return e.Code == want.Code })
		if i < 0 {
			t.Fatalf("%s is not in the catalog", want.Code)
		}
		if codes[i].Kind != "service" || !appCode(want.Code) {
			t.Errorf("%s: kind %q, an App may pass it on: %v", want.Code, codes[i].Kind, appCode(want.Code))
		}
		if texts[i]["en"] != want.Message {
			t.Errorf("%s: message %q, catalog en %q", want.Code, want.Message, texts[i]["en"])
		}
		for _, locale := range catalogLocales {
			if s, _ := texts[i][locale].(string); s == "" {
				t.Errorf("%s: no %s text", want.Code, locale)
			}
		}
	}
	// api codes the browser reads in its own words; the api message stays a
	// developer's text.
	apiCodes := []string{"request_timeout"}
	for _, code := range apiCodes {
		i := slices.IndexFunc(codes, func(e catalogEntry) bool { return e.Code == code })
		if i < 0 {
			t.Fatalf("%s is not in the catalog", code)
		}
		if codes[i].Kind != "refusal" || !reservedCodes[code] {
			t.Errorf("%s: kind %q, reserved %v", code, codes[i].Kind, reservedCodes[code])
		}
		for _, locale := range catalogLocales {
			if s, _ := texts[i][locale].(string); s == "" {
				t.Errorf("%s: no %s text", code, locale)
			}
		}
	}
	// Executor client codes an App passes on, which the browser retries and
	// shows in its own words.
	clientCodes := []string{"APP_STOPPED", "APP_STARTING"}
	for _, code := range clientCodes {
		i := slices.IndexFunc(codes, func(e catalogEntry) bool { return e.Code == code })
		if i < 0 {
			t.Fatalf("%s is not in the catalog", code)
		}
		if codes[i].Kind != "service" || !codes[i].Retryable || !appCode(code) {
			t.Errorf("%s: kind %q, retryable %v, an App may pass it on: %v", code, codes[i].Kind, codes[i].Retryable, appCode(code))
		}
		for _, locale := range catalogLocales {
			if s, _ := texts[i][locale].(string); s == "" {
				t.Errorf("%s: no %s text", code, locale)
			}
		}
	}
	if want := len(runtime) + len(services) + len(apiCodes) + len(clientCodes); len(codes) != want {
		t.Errorf("catalog lists %d codes, want %d", len(codes), want)
	}
}
