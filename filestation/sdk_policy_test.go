package filestation

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// Every files entry in the SDK policy must name a route this package serves.
func TestSDKPolicyListsExactlyTheFileRoutes(t *testing.T) {
	raw, err := os.ReadFile("../api/sdk-permissions.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	var declared, routed []string
	for method := range policy.Files {
		declared = append(declared, method)
	}
	for _, route := range (&Service{}).Routes() {
		routed = append(routed, route.Method())
	}
	sort.Strings(declared)
	sort.Strings(routed)
	if len(declared) != len(routed) {
		t.Fatalf("policy %v, routes %v", declared, routed)
	}
	for i := range declared {
		if declared[i] != routed[i] {
			t.Fatalf("policy %v, routes %v", declared, routed)
		}
	}
}
