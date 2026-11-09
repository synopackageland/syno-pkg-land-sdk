package main

import (
	"os"
	"strings"
	"testing"
)

func TestConstantsRequireExplicitAdmissionMetadata(t *testing.T) {
	for _, catalog := range []string{
		`{"codes":[{"code":"AUTH_DENIED","kind":"refusal"}]}`,
		`{"codes":[{"code":"EXECUTOR_TIMEOUT","kind":"client","preAdmission":true}]}`,
	} {
		if _, err := constants([]byte(catalog)); err == nil {
			t.Fatal("accepted ambiguous admission metadata")
		}
	}
}

func TestAdmissionConstantsMatchCatalog(t *testing.T) {
	catalog, err := os.ReadFile("../../spec/protocol/v1/errors.json")
	if err != nil {
		t.Fatal(err)
	}
	generated, err := constants(catalog)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../../delegate/codes_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if string(generated) != string(committed) {
		t.Fatal("stale delegate codes; run go generate ./delegate")
	}
	if !strings.Contains(string(generated), `"AUTH_DENIED":`) {
		t.Fatal("missing admission evidence")
	}
}
