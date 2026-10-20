package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A schema edit must be rejected before regeneration can make incompatible
// Go/TS artifacts look current. This includes the reviewer's Subject.uid case.
func TestRejectsIncompatibleGoTypeSchema(t *testing.T) {
	for _, goType := range []string{"json.Number", "int64", "uint64"} {
		for _, tc := range []struct {
			name string
			edit func(*schema)
		}{
			{"string", func(s *schema) { s.Type = "string" }},
			{"boolean", func(s *schema) { s.Type = "boolean" }},
			{"number", func(s *schema) { s.Type = "number" }},
			{"missing type", func(s *schema) { s.Type = "" }},
			{"reference", func(s *schema) { s.Ref = "#/$defs/ScopeSelection" }},
			{"nullable wrapper", func(s *schema) {
				s.Type = ""
				s.AnyOf = []schema{{Type: "string"}, {Type: "null"}}
			}},
			{"string enum", func(s *schema) { s.Enum = []string{"42"} }},
			{"string constant", func(s *schema) { value := "42"; s.Const = &value }},
		} {
			t.Run(goType+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				original := make(map[string][]byte)
				for _, path := range []string{"spec/delegate/v1/dtos.schema.json", "delegate/dtos_gen.go", "ts/src/delegate/dtos-gen.ts"} {
					b, err := os.ReadFile(filepath.Join("../..", path))
					if err != nil {
						t.Fatal(err)
					}
					original[path] = b
					if strings.HasPrefix(path, "spec/") {
						var doc schema
						if err := json.Unmarshal(b, &doc); err != nil {
							t.Fatal(err)
						}
						uid := doc.Defs["Subject"].Properties["uid"]
						uid.GoType = goType
						tc.edit(&uid)
						doc.Defs["Subject"].Properties["uid"] = uid
						b, err = json.Marshal(doc)
						if err != nil {
							t.Fatal(err)
						}
					}
					dest := filepath.Join(root, path)
					if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(dest, b, 0644); err != nil {
						t.Fatal(err)
					}
				}
				for _, check := range []bool{false, true} {
					err := run(root, check)
					if err == nil || !strings.Contains(err.Error(), "Subject.uid") || !strings.Contains(err.Error(), "incompatible x-go-type") {
						t.Errorf("check=%v: incompatible schema must be rejected, got %v", check, err)
					}
				}
				for _, path := range []string{"delegate/dtos_gen.go", "ts/src/delegate/dtos-gen.ts"} {
					got, err := os.ReadFile(filepath.Join(root, path))
					if err != nil || !bytes.Equal(got, original[path]) {
						t.Errorf("invalid schema overwrote %s", path)
					}
				}
			})
		}
	}
}

// Run with the root Go tests: changing either language or the spec must fail
// until both artifacts have been regenerated from the same source.
func TestDelegateDTOsAreCurrent(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "--check", "../..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("delegate DTO contract: %v\n%s", err, out)
	}
}

func TestCheckRejectsContractDrift(t *testing.T) {
	for _, tc := range []struct{ name, path, from, to string }{
		{"Go field type", "delegate/dtos_gen.go", "Path string", "Path bool"},
		{"Go JSON name", "delegate/dtos_gen.go", "json:\"path\"", "json:\"destination_path\""},
		{"Go omission", "delegate/dtos_gen.go", "receipt,omitempty", "receipt"},
		{"TS field name", "ts/src/delegate/dtos-gen.ts", " path: string", " destination_path: string"},
		{"TS field type", "ts/src/delegate/dtos-gen.ts", " size: number", " size: string"},
		{"TS optional", "ts/src/delegate/dtos-gen.ts", " receipt?: Receipt", " receipt: Receipt"},
		{"TS nullable", "ts/src/delegate/dtos-gen.ts", "selection: ScopeSelection | null", "selection: ScopeSelection"},
		{"TS enum", "ts/src/delegate/dtos-gen.ts", "\"uncertain\"", "\"failed\""},
		{"spec change", "spec/delegate/v1/dtos.schema.json", "\"until-revoked\"", "\"timed\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, path := range []string{"spec/delegate/v1/dtos.schema.json", "delegate/dtos_gen.go", "ts/src/delegate/dtos-gen.ts"} {
				b, err := os.ReadFile(filepath.Join("../..", path))
				if err != nil {
					t.Fatal(err)
				}
				if path == tc.path {
					if !strings.Contains(string(b), tc.from) {
						t.Fatalf("mutation anchor %q missing", tc.from)
					}
					b = []byte(strings.Replace(string(b), tc.from, tc.to, 1))
				}
				dest := filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dest, b, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := run(root, true); err == nil || !strings.Contains(err.Error(), "is stale") {
				t.Fatalf("contract drift must be rejected, got %v", err)
			}
		})
	}
}
