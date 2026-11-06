package nativehttp

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/synopackageland/syno-pkg-land-sdk/jsonlimits"
)

//go:embed schema/*.json
var schemas embed.FS

// Canonical implements RFC 8785 (including UTF-16 key order and ECMAScript numbers).
// Invalid Unicode, duplicate keys and nesting beyond 32 are rejected first.
func Canonical(raw []byte) ([]byte, error) {
	depth := 0
	quoted, escaped := false, false
	for _, b := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		if b == '"' {
			quoted = true
		}
		if b == '{' || b == '[' {
			depth++
			if depth > jsonlimits.MaxDepth {
				return nil, errors.New("metadata nesting exceeds 32")
			}
		}
		if b == '}' || b == ']' {
			depth--
		}
	}
	if !json.Valid(raw) {
		return nil, errors.New("invalid metadata JSON")
	}
	return jsoncanonicalizer.Transform(raw)
}
func Digest(raw []byte) (string, error) {
	b, e := Canonical(raw)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
func digestValue(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return Digest(raw)
}

var compiled = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(nil)
	for _, name := range []string{"registry", "descriptor", "inventory"} {
		raw, err := schemas.ReadFile("schema/" + name + ".schema.json")
		if err != nil {
			return nil, err
		}
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if err = c.AddResource(schemaURL(name), v); err != nil {
			return nil, err
		}
	}
	out := map[string]*jsonschema.Schema{}
	for _, name := range []string{"registry", "descriptor", "inventory"} {
		s, e := c.Compile(schemaURL(name))
		if e != nil {
			return nil, e
		}
		out[name] = s
	}
	return out, nil
})

func schemaURL(name string) string {
	return "https://synopkgland.org/schema/api/v3/" + name + ".schema.json"
}

// ValidateShape enforces a bundled formal schema. No external resource is loaded.
// Semantic checks remain mandatory; consumers should use ParseRegistry/Export/ValidateArtifacts.
func ValidateShape(name string, raw []byte) error {
	b, e := Canonical(raw)
	if e != nil {
		return e
	}
	all, e := compiled()
	if e != nil {
		return e
	}
	s, ok := all[name]
	if !ok {
		return errors.New("unknown metadata schema")
	}
	v, e := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if e != nil {
		return e
	}
	if e = s.Validate(v); e != nil {
		return fmt.Errorf("invalid %s metadata: %w", name, e)
	}
	return nil
}
