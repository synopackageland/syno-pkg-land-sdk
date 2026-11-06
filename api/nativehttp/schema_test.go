package nativehttp

import (
	"encoding/json"
	"os"
	"testing"
)

func fixture(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/" + path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestSchemaConformance(t *testing.T) {
	var cases struct {
		Valid   []string
		Invalid []struct{ File, Stage string }
	}
	if e := json.Unmarshal(fixture(t, "cases.json"), &cases); e != nil {
		t.Fatal(e)
	}
	for _, path := range cases.Valid {
		t.Run(path, func(t *testing.T) {
			if e := ValidateShape("registry", fixture(t, path)); e != nil {
				t.Fatal(e)
			}
		})
	}
	for _, tc := range cases.Invalid {
		if tc.Stage != "schema" {
			continue
		}
		t.Run(tc.File, func(t *testing.T) {
			if e := ValidateShape("registry", fixture(t, tc.File)); e == nil {
				t.Fatal("invalid shape accepted")
			}
		})
	}
}
func TestCanonicalRejectsInvalidJSON(t *testing.T) {
	for _, s := range []string{`{"x":1,"x":2}`, `{"x":"\ud800"}`, `{"x":"\udfff"}`, `{"x":1e999}`, "{\"x\":\"\xff\"}"} {
		if _, e := Canonical([]byte(s)); e == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
func TestCanonicalVectors(t *testing.T) {
	var vectors []struct{ Input, Canonical, SHA256 string }
	if e := json.Unmarshal(fixture(t, "canonical-vectors.json"), &vectors); e != nil {
		t.Fatal(e)
	}
	for _, v := range vectors {
		b, e := Canonical([]byte(v.Input))
		if e != nil || string(b) != v.Canonical {
			t.Fatalf("canonical mismatch: %s %v", b, e)
		}
		h, e := Digest([]byte(v.Input))
		if e != nil || h != v.SHA256 {
			t.Fatalf("digest mismatch: %s %v", h, e)
		}
	}
}
