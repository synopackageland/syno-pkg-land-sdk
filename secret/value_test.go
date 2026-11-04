package secret

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestValueNeverSerializesPlaintext(t *testing.T) {
	var params struct {
		Credential Value `json:"credential"`
	}
	if err := json.Unmarshal([]byte(`{"credential":"canary-secret"}`), &params); err != nil {
		t.Fatal(err)
	}
	if params.Credential.Reveal() != "canary-secret" {
		t.Fatal("request was not decoded")
	}
	for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q", "%x"} {
		if got := fmt.Sprintf(format, params.Credential); strings.Contains(got, "canary-secret") {
			t.Fatal(got)
		}
	}
	raw, err := json.Marshal(params)
	if err != nil || string(raw) != `{"credential":"[redacted]"}` {
		t.Fatalf("%s %v", raw, err)
	}
	var dump bytes.Buffer
	slog.New(slog.NewJSONHandler(&dump, nil)).Info("request", "credential", params.Credential)
	if strings.Contains(dump.String(), "canary-secret") {
		t.Fatal(dump.String())
	}
	dump.Reset()
	if err := gob.NewEncoder(&dump).Encode(params); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(dump.Bytes(), []byte("canary-secret")) {
		t.Fatal("gob leaked")
	}
	params.Credential.Clear()
	if params.Credential.Reveal() != "" {
		t.Fatal("not cleared")
	}
}

func TestRedactNestedAndRequestDump(t *testing.T) {
	value := map[string]any{"Password": "canary-p", "nested": []any{map[string]any{"token": "canary-t", "X-SYNO-TOKEN": "canary-s", "otp": "canary-o", "Cookie": "canary-c"}}, "safe": "visible", "custom": New("canary-v")}
	raw, err := json.Marshal(Redact(value))
	if err != nil || bytes.Contains(raw, []byte("canary-")) || !bytes.Contains(raw, []byte("visible")) {
		t.Fatalf("%s %v", raw, err)
	}
}

func TestRedactorProtectsCredentialKeysAndOverlappingValues(t *testing.T) {
	r := NewRedactor(json.RawMessage(`{"credential":["long-canary","canary"],"otp":123456}`), "credential")
	safe, _ := json.Marshal(r.Redact(map[string]any{"long-canary": "long-canary canary 123456"}))
	if strings.Contains(string(safe), "canary") || strings.Contains(string(safe), "123456") {
		t.Fatal(string(safe))
	}
}

func TestValueRejectsNullAndCannotRestoreRedaction(t *testing.T) {
	for _, raw := range []string{`null`, ` null `, `"[redacted]"`, `123`, `{}`} {
		value := New("canary")
		copy := value
		if json.Unmarshal([]byte(raw), &value) == nil {
			t.Errorf("accepted %s", raw)
		}
		if copy.Reveal() != "" {
			t.Fatal("failed decode retained old credential")
		}
	}
}

func TestDiagnosticRedactionUsesExactNames(t *testing.T) {
	input := map[string]any{}
	for _, key := range []string{"password", "passwd", "passphrase", "otp", "token", "secret", "authorization", "cookie", "synotoken", "sid", "Password", "X-SYNO-TOKEN", "appCredential"} {
		input[key] = "hidden"
	}
	for _, key := range []string{"sidebar", "tokenCount", "secretName", "otpEnabled", "api_token", "passwordHint", "side_bar"} {
		input[key] = "visible"
	}
	got := NewRedactor(nil, "appCredential").Redact(input).(map[string]any)
	for key, value := range input {
		want := any(Redacted)
		if value == "visible" {
			want = value
		}
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	if input["password"] != "hidden" {
		t.Fatal("diagnostic copy mutated source")
	}
}
