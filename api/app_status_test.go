package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestAppStatusLanguageAndIdentity(t *testing.T) {
	caller := Caller{identity: Identity{User: "alice", Admin: false}, authenticated: true}
	for _, tc := range []struct{ preference, header, lang, locale string }{
		{"cht", "en-US", "cht", "zh-TW"},
		{"", "ja-JP, en;q=0.8", "", "ja-JP"},
		{"", "bad<script>", "", ""},
		{"", "", "", ""},
	} {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("Accept-Language", tc.header)
		handler := AppStatusHandler("ExampleApp", "1", func(context.Context, Caller) (string, error) { return tc.preference, nil })
		value, err := handler(context.Background(), caller, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		status := value.(AppStatus)
		if status.User != "alice" || status.Admin || status.Lang != tc.lang || status.Locale != tc.locale {
			t.Fatalf("unexpected status: %+v", status)
		}
		raw, _ := json.Marshal(status)
		var wire map[string]any
		json.Unmarshal(raw, &wire)
		if _, ok := wire["lang"]; !ok {
			t.Fatal("missing language field")
		}
		if _, ok := wire["locale"]; !ok {
			t.Fatal("missing locale field")
		}
	}
	_, err := AppStatusHandler("ExampleApp", "1", nil)(context.Background(), Caller{}, nil, nil)
	if err == nil {
		t.Fatal("unauthenticated status succeeded")
	}
}
