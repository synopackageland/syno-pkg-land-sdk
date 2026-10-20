package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type opSchema struct {
	Schema     string                     `json:"$schema"`
	Since      string                     `json:"since"`
	Properties map[string]json.RawMessage `json:"properties"`
	Required   []string                   `json:"required"`
	Additional *bool                      `json:"additionalProperties"`
}

// executorOps reads allOps from the executor source, the protocol's op list.
func executorOps(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("../executor/executor.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`var allOps = \[\]string\{([^}]*)\}`).FindSubmatch(b)
	if m == nil {
		t.Fatal("allOps not found")
	}
	var ops []string
	for _, op := range strings.Split(string(m[1]), ",") {
		ops = append(ops, strings.Trim(strings.TrimSpace(op), `"`))
	}
	return ops
}

func loadSchemas(t *testing.T) map[string]opSchema {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(specDir, "schema", "*.json"))
	out := map[string]opSchema{}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var s opSchema
		if err = json.Unmarshal(b, &s); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		out[strings.TrimSuffix(filepath.Base(file), ".json")] = s
	}
	return out
}

// sinceRE is an SDK release from 0.9.0 on; new ops carry the next one.
var sinceRE = regexp.MustCompile(`^0\.(9|10|11)\.[0-9]+$`)

func TestOneSchemaPerOp(t *testing.T) {
	schemas := loadSchemas(t)
	ops := executorOps(t)
	for _, op := range ops {
		s, ok := schemas[op]
		if !ok {
			t.Errorf("no schema for %s", op)
			continue
		}
		if s.Schema != "https://json-schema.org/draft/2020-12/schema" || !sinceRE.MatchString(s.Since) || s.Additional == nil || *s.Additional {
			t.Errorf("%s: want draft 2020-12, since an SDK release, additionalProperties false", op)
		}
	}
	for op := range schemas {
		if !slices.Contains(ops, op) {
			t.Errorf("schema for unknown op %s", op)
		}
	}
}

func TestAcceptedVectorsFollowTheirSchema(t *testing.T) {
	schemas := loadSchemas(t)
	for name, v := range loadVectors(t) {
		var r struct {
			Op     string                     `json:"op"`
			Params map[string]json.RawMessage `json:"params"`
		}
		var e struct {
			OK bool `json:"ok"`
		}
		if json.Unmarshal(v.Request, &r) != nil || json.Unmarshal(v.Expect, &e) != nil || !e.OK {
			continue
		}
		s := schemas[r.Op]
		for k := range r.Params {
			if _, ok := s.Properties[k]; !ok {
				t.Errorf("%s: %s is not in the %s schema", name, k, r.Op)
			}
		}
		for _, k := range s.Required {
			if _, ok := r.Params[k]; !ok {
				t.Errorf("%s: required %s missing", name, k)
			}
		}
	}
}
