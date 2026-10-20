package jsonlimits

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExactStructureAndDepthBounds(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		ok   bool
	}{
		"structure at limit":   {"[" + strings.Repeat("0,", MaxStructuralBytes-2) + "0]", true},
		"structure over limit": {"[" + strings.Repeat("0,", MaxStructuralBytes-1) + "0]", false},
		"depth at limit":       {strings.Repeat("[", MaxDepth) + "0" + strings.Repeat("]", MaxDepth), true},
		"depth over limit":     {strings.Repeat("[", MaxDepth+1) + "0" + strings.Repeat("]", MaxDepth+1), false},
		"object punctuation":   {"{" + strings.Repeat(`"x":0,`, MaxStructuralBytes/2) + `"x":0}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			if !json.Valid([]byte(tc.body)) {
				t.Fatal("invalid fixture")
			}
			if WithinLimits([]byte(tc.body)) != tc.ok {
				t.Fatalf("want allowed=%v", tc.ok)
			}
		})
	}
}

func TestStringEscapesCannotHideFollowingStructure(t *testing.T) {
	for _, value := range []string{`\`, `"`, `\"`, `[]{}:,`, "\u4f60\u597d"} {
		encoded, err := json.Marshal(strings.Repeat(value, 65536))
		if err != nil {
			t.Fatal(err)
		}
		if !WithinLimits(encoded) {
			t.Fatal("string contents counted as structure")
		}
		body := append(append([]byte(`{"text":`), encoded...), []byte(`,"array":[`+strings.Repeat("0,", MaxStructuralBytes)+`0]}`)...)
		if !json.Valid(body) || WithinLimits(body) {
			t.Fatal("escaped string hid oversized array")
		}
	}
}

func TestScannerDoesNotAllocate(t *testing.T) {
	body := []byte("[" + strings.Repeat("0,", MaxStructuralBytes-2) + "0]")
	if allocations := testing.AllocsPerRun(10, func() { WithinLimits(body) }); allocations != 0 {
		t.Fatalf("allocations=%v", allocations)
	}
}
