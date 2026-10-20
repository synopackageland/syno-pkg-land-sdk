package main

import (
	"testing"
)

func TestReviewCaseFoldedDuplicateAuthRejected(t *testing.T) {
	for _, raw := range []string{
		`{"v":1,"op":"delegate.list","auth":{"dsm_cookie":"a","DSM_COOKIE":"b","dsm_synotoken":"token"},"params":{}}`,
		`{"v":1,"op":"delegate.list","auth":{"DSM_COOKIE":"a","dsm_synotoken":"token"},"params":{}}`,
		`{"v":1,"op":"delegate.list","AUTH":{"dsm_cookie":"a","dsm_synotoken":"token"},"params":{}}`,
		`{"v":1,"op":"files.list","auth":{},"params":{"path":"/home","PATH":"/other"}}`,
		`{"v":1,"op":"files.list","auth":{},"params":{"PATH":"/home"}}`,
		`{"v":1,"op":"files.list","auth":{},"Params":{}}`,
	} {
		var req Request
		if strictJSON([]byte(raw), &req) == nil {
			t.Errorf("ambiguous or case variant accepted: %s", raw)
		}
	}
}
func TestStrictJSONCaseInsensitiveKeysIncludeUnicodeFolds(t *testing.T) {
	for _, raw := range []string{`{"key":1,"KEY":2}`, `{"s":1,"ſ":2}`, `{"k":1,"K":2}`} {
		var out map[string]any
		if strictJSON([]byte(raw), &out) == nil {
			t.Errorf("case duplicate accepted: %s", raw)
		}
	}
}

func TestStrictJSONDirectCredentialAndParamsKeysAreCanonical(t *testing.T) {
	var auth Auth
	var params Params
	if strictJSON([]byte(`{"DSM_COOKIE":"a"}`), &auth) == nil {
		t.Fatal("uppercase direct auth key")
	}
	if strictJSON([]byte(`{"PATH":"/home"}`), &params) == nil {
		t.Fatal("uppercase direct params key")
	}
}
