package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sort"
)

// DescriptionMethod is the one authenticated discovery operation.
const DescriptionMethod = "synopkgland.describe"
const DescriptionFile = "synopkgland-api.json"

// WithDescription serves only the fixed discovery method using DSM auth, even
// outside a standalone App session. next retains all business/session guards.
// filename is a trusted private package path, never a request parameter.
func WithDescription(next http.Handler, auth Authenticator, filename string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Query().Get("method") != "" {
			next.ServeHTTP(w, r)
			return
		}
		original := r.Body
		prefix, err := io.ReadAll(io.LimitReader(original, MaxRequestBytes+1))
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(prefix), original))
		var peek struct {
			Method string `json:"method"`
		}
		if err != nil || len(prefix) > MaxRequestBytes || json.Unmarshal(prefix, &peek) != nil || peek.Method != DescriptionMethod {
			next.ServeHTTP(w, r)
			return
		}
		defer original.Close()
		req, e := readRequest(w, r, MaxRequestBytes)
		if e != nil {
			writeError(w, e)
			return
		}
		if _, err := DSMSession(r); err != nil {
			writeError(w, asError(err))
			return
		}
		if auth == nil {
			writeError(w, &Error{"auth_unavailable", "DSM authentication unavailable"})
			return
		}
		_, err = authenticateEntry(auth, expectedEntryMode, r)
		switch {
		case errors.Is(err, ErrNoSession):
			writeError(w, &Error{"session_required", "sign in to DSM again"})
			return
		case errors.Is(err, ErrAdminRequired):
			writeError(w, adminRequired)
			return
		case errors.Is(err, ErrAppPrivilegeDenied):
			writeError(w, &Error{"app_privilege_denied", "App access denied"})
			return
		case err != nil:
			writeError(w, &Error{"auth_unavailable", "DSM authentication unavailable"})
			return
		}
		var params map[string]json.RawMessage
		if len(req.Params) == 0 || json.Unmarshal(req.Params, &params) != nil || params == nil || len(params) != 0 {
			writeError(w, BadRequest("describe params must be {}"))
			return
		}
		file, err := os.Open(filename)
		if err != nil {
			writeError(w, &Error{"unknown_method", "API description not installed"})
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > MaxRequestBytes {
			writeError(w, &Error{"description_invalid", "API description unavailable"})
			return
		}
		raw, err := io.ReadAll(io.LimitReader(file, MaxRequestBytes+1))
		if err != nil || len(raw) > MaxRequestBytes || !json.Valid(raw) {
			writeError(w, &Error{"description_invalid", "API description unavailable"})
			return
		}
		WriteData(w, json.RawMessage(raw))
	})
}

// DescribeRoutes exports actual routes with their effective central policy.
// Build tools call it before any handler/migration is run; transfers are marked so build tools can bind a fixed upload adapter.
func DescribeRoutes(raw []byte, routes ...Route) ([]map[string]string, error) {
	if err := checkAppPolicy(raw, routes); err != nil {
		return nil, err
	}
	p, err := parsePermissions(raw)
	if err != nil {
		return nil, err
	}
	registry, err := buildRegistry(routes)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []map[string]string{}
	for _, name := range names {
		r := registry[name]
		if _, owned := sdkPolicy(name); !owned && expectedPermissionsDigest != "" {
			if _, declared := p.methods[name]; !declared {
				return nil, errors.New("registered API method missing from permissions: " + name)
			}
		}
		kind := "json"
		if r.streamHandler != nil {
			kind = "transfer"
		}

		rule := p.forMethod(name)
		if fixed, ok := sdkPolicy(name); ok {
			rule = fixed
		}
		permission := "administrators"
		if rule == policyUser {
			permission = "authenticated"
		}
		if rule == policyPublic {
			permission = "public"
		}
		out = append(out, map[string]string{"method": name, "permission": permission, "kind": kind})
	}
	return out, nil
}
