package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestJSONHasExactCGIResponseLength(t *testing.T) {
	for _, value := range []any{map[string]any{"success": true, "data": "\u8cc7\u6599\u593e"}, map[string]any{"success": false, "error": "denied"}, make(chan int)} {
		w := httptest.NewRecorder()
		writeJSON(w, http.StatusOK, value)
		if w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
			t.Fatalf("CGI response missing exact byte length: %v", w.Header())
		}
		if !json.Valid(w.Body.Bytes()) {
			t.Fatal("invalid JSON response")
		}
		if _, unsupported := value.(chan int); unsupported {
			var fallback struct {
				Success bool
				Error   struct{ Code string }
			}
			_ = json.Unmarshal(w.Body.Bytes(), &fallback)
			if w.Code != http.StatusOK || fallback.Success || fallback.Error.Code != "internal" {
				t.Fatal("serialization failure lost DSM JSON refusal envelope")
			}
		}
	}
}
