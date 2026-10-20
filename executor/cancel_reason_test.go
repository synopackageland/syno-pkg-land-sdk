package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestMoveCancelReasonsComeFromTheSpec(t *testing.T) {
	b, err := os.ReadFile("../spec/protocol/v1/errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Codes []struct{ Code, Kind string } `json:"codes"`
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	reasons := map[string]bool{}
	for _, c := range spec.Codes {
		if c.Kind == "reason" {
			reasons[c.Code] = true
		}
	}
	for _, tc := range []struct{ fence, want string }{
		{"STORAGE_ERROR", "storage-error"},
		{"disabled", "integration-disabled"},
		{"DELEGATION_DENIED", "grant-revoked"},
		{"", "validation-denied"},
	} {
		got := moveCancelReason(tc.fence)
		if got != tc.want || !reasons[got] {
			t.Errorf("moveCancelReason(%q) = %q (in spec %v), want %q", tc.fence, got, reasons[got], tc.want)
		}
	}
}
