package compose

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/compose-spec/compose-go/v2/schema"
)

// DSM re-emits the worker file before Compose reads it and drops the quotes of
// numeric-looking scalars, so a string such as "10" or "0755" reaches Compose
// as a number, boolean or null. Compose then rejects it where only strings are
// allowed (healthcheck.test.N must be a string) or changes it silently. Defer
// such literals until Compose interpolation: SYNOPKGLAND_GID is always set and
// non-empty in the runtime .env, so ${SYNOPKGLAND_GID:+10} expands to exactly
// "10", while DSM sees a nonnumeric scalar.
const literalSentinel = "${SYNOPKGLAND_GID:+"

var (
	plainNull  = regexp.MustCompile(`^(~|null|Null|NULL)$`)
	plainBool  = regexp.MustCompile(`^(y|Y|yes|Yes|YES|n|N|no|No|NO|true|True|TRUE|false|False|FALSE|on|On|ON|off|Off|OFF)$`)
	plainInt   = regexp.MustCompile(`^[-+]?(0b[01_]+|0o?[0-7_]+|0x[0-9a-fA-F_]+|[0-9][0-9_]*|[1-9][0-9_]*(:[0-5]?[0-9])+)$`)
	plainFloat = regexp.MustCompile(`^([-+]?(\.[0-9][0-9_]*|[0-9][0-9_]*\.[0-9_]*|[0-9][0-9_]*)([eE][-+]?[0-9]+)?|[-+]?[0-9][0-9_]*(:[0-5]?[0-9])+\.[0-9_]*|[-+]?\.(inf|Inf|INF)|\.(nan|NaN|NAN))$`)
	// Compose turns these back into the same text where its schema accepts
	// the type DSM produces.
	canonicalInt = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,14})$`)
)

// plainNonString reports whether a YAML 1.1 or 1.2 parser reads s, unquoted,
// as something other than the string s.
func plainNonString(s string) bool {
	return plainNull.MatchString(s) || plainBool.MatchString(s) || plainInt.MatchString(s) || plainFloat.MatchString(s)
}

// deferLiteral reports whether the string value at path must be deferred.
// Canonical integers and true/false stay where the Compose schema accepts a
// number or boolean (environment, labels, sizes), so Apps that do not need the
// deferral keep the same worker bytes.
func deferLiteral(value string, path []string) bool {
	if !plainNonString(value) {
		return false
	}
	switch {
	case canonicalInt.MatchString(value):
		types := schemaTypes(path)
		return !types["number"] && !types["integer"]
	case value == "true" || value == "false":
		return !schemaTypes(path)["boolean"]
	}
	return true
}

var composeSchema = sync.OnceValue(func() map[string]any {
	var s map[string]any
	if err := json.Unmarshal([]byte(schema.Schema), &s); err != nil {
		return nil
	}
	return s
})

// schemaTypes returns the JSON types the Compose schema accepts at path. An
// unknown path accepts nothing, which defers the value.
func schemaTypes(path []string) map[string]bool {
	root := composeSchema()
	out := map[string]bool{}
	if root == nil {
		return out
	}
	var visit func(node map[string]any, path []string, depth int)
	visit = func(node map[string]any, path []string, depth int) {
		if node == nil || depth > 64 {
			return
		}
		if ref, ok := node["$ref"].(string); ok {
			if name, ok := strings.CutPrefix(ref, "#/$defs/"); ok {
				defs, _ := root["$defs"].(map[string]any)
				def, _ := defs[name].(map[string]any)
				visit(def, path, depth+1)
			}
		}
		for _, key := range []string{"oneOf", "anyOf", "allOf"} {
			branches, _ := node[key].([]any)
			for _, b := range branches {
				branch, _ := b.(map[string]any)
				visit(branch, path, depth+1)
			}
		}
		if len(path) == 0 {
			switch t := node["type"].(type) {
			case string:
				out[t] = true
			case []any:
				for _, v := range t {
					if name, ok := v.(string); ok {
						out[name] = true
					}
				}
			}
			return
		}
		step, rest := path[0], path[1:]
		if _, err := strconv.Atoi(step); err == nil {
			if items, ok := node["items"].(map[string]any); ok {
				visit(items, rest, depth+1)
			}
			return
		}
		if props, ok := node["properties"].(map[string]any); ok {
			if child, ok := props[step].(map[string]any); ok {
				visit(child, rest, depth+1)
				return
			}
		}
		matched := false
		if patterns, ok := node["patternProperties"].(map[string]any); ok {
			for pattern, child := range patterns {
				if re, err := regexp.Compile(pattern); err == nil && re.MatchString(step) {
					matched = true
					c, _ := child.(map[string]any)
					visit(c, rest, depth+1)
				}
			}
		}
		if !matched {
			if extra, ok := node["additionalProperties"].(map[string]any); ok {
				visit(extra, rest, depth+1)
			}
		}
	}
	visit(root, path, 0)
	return out
}
