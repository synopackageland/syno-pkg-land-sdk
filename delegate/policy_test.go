package delegate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// The SDK policy grants every delegate route to signed-in users, except the
// administrator switch, and the delegate picker browses shares the same way.
func TestSDKPolicyCoversEveryDelegateRoute(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "api", "sdk-permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]map[string]string
	if err = json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	var routes, admin []string
	for _, route := range (&Service{}).Routes() {
		routes = append(routes, route.Method())
		if route.Public() {
			t.Error("public route", route.Method())
		}
		if route.AdminOnly() {
			admin = append(admin, route.Method())
		}
	}
	var granted []string
	for method, role := range policy["delegate"] {
		granted = append(granted, method)
		if want := map[bool]string{true: "administrators", false: "authenticated"}[method == "delegate.disable" || method == "delegate.enable"]; role != want {
			t.Errorf("%s: %s, want %s", method, role, want)
		}
	}
	sort.Strings(routes)
	sort.Strings(granted)
	sort.Strings(admin)
	if !reflect.DeepEqual(routes, granted) {
		t.Errorf("routes %v, policy %v", routes, granted)
	}
	if !reflect.DeepEqual(admin, []string{"delegate.disable", "delegate.enable"}) {
		t.Error("administrator routes", admin)
	}
	if policy["files"]["nas.files.shares"] != "authenticated" {
		t.Error("the delegate picker cannot list shares")
	}
}
