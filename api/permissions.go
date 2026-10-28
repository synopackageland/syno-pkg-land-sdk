package api

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// AdministratorPermission names a policy rule. DSM's native SDK decides the
// role; this token is never compared with a group or account name.
const AdministratorPermission = "administrators"
const MaxPermissionsBytes = 65536
const MaxPermissionMethods = 1024

// SDK rules are immutable; App JSON only configures App methods.
//
//go:embed sdk-permissions.json
var sdkPermissionsJSON []byte

func sdkPolicy(method string) (policy, bool) {
	var capabilities map[string]map[string]string
	if err := json.Unmarshal(sdkPermissionsJSON, &capabilities); err != nil {
		panic(err)
	}
	for _, methods := range capabilities {
		if value, ok := methods[method]; ok {
			rule, valid := permissionValue(value)
			if !valid {
				panic("invalid SDK policy")
			}
			return rule, true
		}
	}
	return policyUnset, false
}

// Set by the CLI through -ldflags -X to bind the SDK policy it validated to
// the compiled CGI. Empty is permitted for direct Go development builds only.
var expectedSDKPermissionsDigest string

// The CLI binds these alongside the SDK digest for every packaged App. The
// entry floor therefore survives a missing wrapper in a Go application's main.
var expectedEntryMode, expectedPermissionsDigest string

type permissions struct {
	methods map[string]policy
}

func permissionValue(value string) (policy, bool) {
	switch value {
	case AdministratorPermission:
		return policyAdmin, true
	case "authenticated":
		return policyUser, true
	case "public":
		return policyPublic, true
	}
	return policyUnset, false
}

func (p permissions) forMethod(method string) policy {
	if rule, ok := p.methods[method]; ok {
		return rule
	}
	return policyAdmin
}

// Parse only this fixed JSON shape; aliases, duplicates, missing fields and
// unknown permission names must fail closed instead of falling back to User.
func parsePermissions(raw []byte) (permissions, error) {
	invalid := func() (permissions, error) { return permissions{}, fmt.Errorf("invalid API permissions JSON") }
	if len(raw) > MaxPermissionsBytes || !utf8.Valid(raw) {
		return invalid()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return invalid()
	}
	p := permissions{methods: map[string]policy{}}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return invalid()
		}
		seen[key] = true
		switch key {
		case "schema":
			var schema float64
			if d.Decode(&schema) != nil || schema != 1 {
				return invalid()
			}
		case "default":
			var value string
			if d.Decode(&value) != nil || value != AdministratorPermission {
				return invalid()
			}
		case "methods":
			if token, err := d.Token(); err != nil || token != json.Delim('{') {
				return invalid()
			}
			for d.More() {
				token, err := d.Token()
				method, ok := token.(string)
				if err != nil || !ok || len(method) > 128 || !methodName.MatchString(method) || len(p.methods) >= MaxPermissionMethods {
					return invalid()
				}
				if _, duplicate := p.methods[method]; duplicate {
					return invalid()
				}
				var value string
				if d.Decode(&value) != nil {
					return invalid()
				}
				rule, ok := permissionValue(value)
				if !ok {
					return invalid()
				}
				p.methods[method] = rule
			}
			if token, err := d.Token(); err != nil || token != json.Delim('}') {
				return invalid()
			}
		default:
			return invalid()
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(seen) != 3 || d.Decode(new(any)) != io.EOF {
		return invalid()
	}
	return p, nil
}

func checkAppPolicy(raw []byte, routes []Route) error {
	if expectedPermissionsDigest == "" {
		return nil
	}
	for _, route := range routes {
		if _, owned := sdkPolicy(route.method); !owned {
			if fmt.Sprintf("%x", sha256.Sum256(raw)) != expectedPermissionsDigest {
				return fmt.Errorf("App permissions do not match package build policy")
			}
			break
		}
	}
	return nil
}

func checkSDKPolicy() error {
	if expectedSDKPermissionsDigest != "" && fmt.Sprintf("%x", sha256.Sum256(sdkPermissionsJSON)) != expectedSDKPermissionsDigest {
		return fmt.Errorf("SDK policy does not match package build policy")
	}
	return nil
}

// declaredPermission is the SDK rule for an SDK method; before a server
// applies the App policy every other method is administrators-only.
func (r Route) declaredPermission() policy {
	if r.policy != policyUnset {
		return r.policy
	}
	if rule, ok := sdkPolicy(r.method); ok {
		return rule
	}
	return policyAdmin
}
