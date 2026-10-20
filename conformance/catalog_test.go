package conformance

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type catalogEntry struct {
	Code       string `json:"code"`
	Kind       string `json:"kind"`
	Retryable  *bool  `json:"retryable"`
	HTTPStatus int    `json:"httpStatus"`
	En         string `json:"en"`
	ZhTW       string `json:"zh-TW"`
}

func loadCatalog(t *testing.T) map[string]catalogEntry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(specDir, "errors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Codes []catalogEntry `json:"codes"`
	}
	if err = json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]catalogEntry{}
	for _, c := range doc.Codes {
		if _, dup := out[c.Code]; dup {
			t.Fatalf("duplicate code %s", c.Code)
		}
		if !slices.Contains([]string{"refusal", "outcome", "reason", "client"}, c.Kind) || c.Retryable == nil || c.HTTPStatus < 200 || c.HTTPStatus > 599 || c.En == "" || c.ZhTW == "" {
			t.Fatalf("incomplete entry %+v", c)
		}
		out[c.Code] = c
	}
	return out
}

// sourceStrings returns every string literal and identifier in the
// hand-written, non-test Go files of dir.
func sourceStrings(t *testing.T, dir string) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || strings.HasSuffix(file, "_gen.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if s, err := strconv.Unquote(n.Value); n.Kind == token.STRING && err == nil {
					out[s] = true
				}
			case *ast.Ident:
				out[n.Name] = true
			}
			return true
		})
	}
	return out
}

func TestEveryExecutorRefusalIsCatalogued(t *testing.T) {
	catalog := loadCatalog(t)
	files, _ := filepath.Glob("../executor/*.go")
	deny := regexp.MustCompile(`deny\("([^"]+)"\)`)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range deny.FindAllSubmatch(b, -1) {
			if c, ok := catalog[string(m[1])]; !ok || c.Kind != "refusal" {
				t.Errorf("%s: deny(%q) is not a refusal in errors.json", file, m[1])
			}
		}
	}
}

func TestEveryCatalogCodeHasAProducer(t *testing.T) {
	executor := sourceStrings(t, "../executor")
	client := sourceStrings(t, "../delegate")
	for code, c := range loadCatalog(t) {
		producer := executor
		if c.Kind == "client" {
			producer = client
		}
		if !producer[code] && !producer[constName(code)] {
			t.Errorf("%s (%s) is not produced by any source", code, c.Kind)
		}
	}
}

func TestVectorRefusalsAreCatalogued(t *testing.T) {
	catalog := loadCatalog(t)
	for name, v := range loadVectors(t) {
		var e struct {
			Code string `json:"code"`
		}
		json.Unmarshal(v.Expect, &e)
		if e.Code != "" && catalog[e.Code].Kind != "refusal" {
			t.Errorf("%s expects %s, which is not a catalogued refusal", name, e.Code)
		}
	}
}

// constName mirrors internal/specgen for refusal and client codes.
func constName(code string) string {
	name := "Code"
	for _, word := range strings.FieldsFunc(code, func(r rune) bool { return r == '_' || r == '-' }) {
		word = strings.ToLower(word)
		name += strings.ToUpper(word[:1]) + word[1:]
	}
	return name
}
