package nativehttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var fixedPath = regexp.MustCompile(`^/[A-Za-z0-9_./~-]+$`)
var templatePath = regexp.MustCompile(`^/[A-Za-z0-9_./{}~-]+$`)
var parameterName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var placeholder = regexp.MustCompile(`^\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

func cleanPath(path string, template bool) bool {
	pattern := fixedPath
	if template {
		pattern = templatePath
	}
	if path == "/" || !pattern.MatchString(path) || strings.Contains(path, "//") {
		return false
	}
	for _, s := range strings.Split(path, "/") {
		if s == "." || s == ".." {
			return false
		}
		if strings.ContainsAny(s, "{}") && (!template || !placeholder.MatchString(s)) {
			return false
		}
	}
	return true
}
func covered(path string, prefixes []string) bool {
	for _, p := range prefixes {
		p = strings.TrimSuffix(p, "/")
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
func validatePrefixes(prefixes []string) error {
	if len(prefixes) < 1 || len(prefixes) > 16 {
		return errors.New("native HTTP requires upstream_auth_paths")
	}
	for i, p := range prefixes {
		if !cleanPath(p, false) || len(p) > 512 || covered(p, []string{"/_sso", "/_spl"}) {
			return errors.New("invalid upstream_auth_paths")
		}
		for _, q := range prefixes[:i] {
			if covered(p, []string{q}) || covered(q, []string{p}) {
				return errors.New("overlapping upstream_auth_paths")
			}
		}
	}
	return nil
}
func credentialPage(page string) bool {
	if strings.HasPrefix(page, "/") || strings.ContainsAny(page, "%\\#\r\n") {
		return false
	}
	u, e := url.Parse(page)
	if e != nil || u.IsAbs() || u.Host != "" {
		return false
	}
	if u.Path != "" && !cleanPath("/"+u.Path, false) {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	for k, values := range q {
		key := strings.ToLower(k)
		if len(values) != 1 || reservedField(key) || strings.Contains(key, "callback") || strings.Contains(key, "redirect") {
			return false
		}
	}
	return true
}
func reservedField(s string) bool {
	s = strings.ToLower(s)
	switch s {
	case "token", "authorization", "cookie", "synotoken", "x-syno-token", "remote-user", "remote_user", "x-remote-user", "x-forwarded-user", "access_token", "api_token", "api_key", "password", "passwd", "credential":
		return true
	}
	return false
}

// ParseRegistry validates both the formal shape and build-context semantics.
// Config is resolved build metadata; no handlers or network requests run.
func ParseRegistry(raw []byte, c BuildConfig) (*Registry, error) {
	if c.Transport != "native-http" {
		return nil, errors.New("native HTTP metadata requires explicit ctl.transport native-http; legacy Compose ctl is unsupported")
	}
	if c.Backend != "compose" && c.Backend != "native" {
		return nil, errors.New("unsupported native HTTP backend")
	}
	if err := c.Gateway.ValidateNativeHTTPMetadata(); err != nil {
		return nil, err
	}
	if c.Package.ID != c.Gateway.Package || c.Gateway.IngressPath == "" {
		return nil, errors.New("native HTTP requires the matching resolved package ingress")
	}
	entry := c.Gateway.Entry
	if entry == "" {
		entry = "policy"
	}
	if entry != "policy" && entry != "admin-only" && entry != "app-privilege" {
		return nil, errors.New("invalid entry mode")
	}
	if c.Gateway.Entry == "policy" || c.Gateway.UpstreamAccess == "public" || c.Gateway.Access == "public" {
		return nil, errors.New("native HTTP cannot bypass DSM entry")
	}
	if err := ValidateShape("registry", raw); err != nil {
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if r.NativeHTTP.BasePath != c.Gateway.IngressPath {
		return nil, errors.New("native HTTP basePath differs from resolved ingress")
	}
	if err := validateRegistry(&r, entry, c.Gateway.UpstreamAuthPaths); err != nil {
		return nil, err
	}
	return &r, nil
}
func validateRegistry(r *Registry, entry string, prefixes []string) error {
	n := r.NativeHTTP
	if !cleanPath(n.BasePath, false) || !strings.HasSuffix(n.BasePath, "/") {
		return errors.New("invalid basePath")
	}
	if err := validatePrefixes(prefixes); err != nil {
		return err
	}
	if !credentialPage(n.Auth.CredentialPage) {
		return errors.New("invalid credentialPage")
	}
	paths := []string{}
	if n.Auth.Type == "header" {
		v := n.Auth.Verify
		paths = append(paths, v.Path)
		if err := validateResponse(v.Response, n.Limits.MaxResponseBytes); err != nil {
			return err
		}
	} else {
		paths = append(paths, n.Auth.ExchangePath, n.Auth.IdentityPath, n.Auth.ActionTokenPath)
	}
	for _, p := range paths {
		if !cleanPath(p, false) || !covered(p, prefixes) {
			return errors.New("auth path outside upstream_auth_paths")
		}
	}
	seen := map[string]bool{}
	for name, op := range r.Operations {
		if name == "synopkgland.describe" || strings.HasPrefix(name, "identity.") {
			return fmt.Errorf("reserved operation: %s", name)
		}
		key := strings.Join(op.CLI, " ")
		if seen[key] {
			return errors.New("duplicate CLI command path")
		}
		seen[key] = true
		if op.Flow.Method != name {
			return fmt.Errorf("flow method differs from operation: %s", name)
		}
		if entry == "admin-only" && op.Permission != "administrators" {
			return errors.New("operation weakens admin-only entry")
		}
		switch op.AI {
		case "read", "preview":
			if op.Effect != "read" {
				return errors.New("read/preview AI operation mutates")
			}
		case "draft":
			p, ok := r.Operations[op.Preview]
			if op.Effect == "read" || !ok || p.AI != "preview" || p.Effect != "read" {
				return errors.New("draft requires an existing nonmutating preview")
			}
		}
		if op.Effect != "read" && op.HTTP.Method == "GET" {
			return errors.New("GET operation must have read effect")
		}
		if op.Preview != "" && op.AI != "draft" {
			return errors.New("preview reference requires draft AI class")
		}
		if n.Auth.Type == "greader-client-login" && (op.HTTP.Path == n.Auth.ExchangePath || op.HTTP.Path == n.Auth.ActionTokenPath) {
			return errors.New("auth ceremony paths cannot be business operations")
		}
		if !cleanPath(op.HTTP.Path, true) || !covered(op.HTTP.Path, prefixes) {
			return fmt.Errorf("operation path outside upstream_auth_paths: %s", name)
		}
		if err := validateOperationSchema(op.ParamsSchema); err != nil {
			return err
		}
		if err := validateOperationSchema(op.ResultSchema); err != nil {
			return err
		}
		var schemaRoot map[string]any
		if e := json.Unmarshal(op.ParamsSchema, &schemaRoot); e != nil {
			return e
		}
		var params struct {
			Properties map[string]map[string]any
			Required   []string
		}
		if e := json.Unmarshal(op.ParamsSchema, &params); e != nil {
			return errors.New("invalid paramsSchema properties")
		}
		for key, value := range params.Properties {
			resolved, err := resolveSchema(value, schemaRoot)
			if err != nil {
				return err
			}
			params.Properties[key] = resolved
		}
		required := map[string]bool{}
		for _, p := range params.Required {
			required[p] = true
		}
		local := ""
		if op.Flow.Type == "upload-http" {
			local = op.Flow.FileParam
		}
		if op.Flow.Type == "download-file" {
			local = op.Flow.OutputParam
		}
		if local != "" {
			s, ok := params.Properties[local]
			if !ok || s["type"] != "string" || s["writeOnly"] != true || !required[local] {
				return errors.New("local file parameter must be a required writeOnly string")
			}
		}
		for _, segment := range strings.Split(op.HTTP.Path, "/") {
			match := placeholder.FindStringSubmatch(segment)
			if len(match) == 0 {
				continue
			}
			p := match[1]
			s, ok := params.Properties[p]
			if !ok || !required[p] || p == local || !scalarSchema(s, false) {
				return errors.New("path placeholder needs a required scalar parameter")
			}
		}
		if (op.HTTP.Method == "GET" || op.HTTP.Method == "DELETE") && op.HTTP.Body != nil {
			return errors.New("GET/DELETE body is unsupported")
		}
		if err := validateBindings(op.HTTP.Query, params.Properties, local, false, n.Auth.Type, false); err != nil {
			return err
		}
		if op.HTTP.Body != nil {
			if len(op.HTTP.Body.Fields) > 16 {
				return errors.New("body fields exceed bound")
			}
			if err := validateBindings(op.HTTP.Body.Fields, params.Properties, local, op.HTTP.Body.Encoding == "json", n.Auth.Type, op.HTTP.Body.Encoding == "form"); err != nil {
				return err
			}
		}
		max := n.Limits.MaxResponseBytes
		if op.Flow.Type == "download-file" {
			max = n.Limits.MaxTransferBytes
		}
		if err := validateResponse(op.HTTP.Response, max); err != nil {
			return err
		}
		if err := validateFlow(op); err != nil {
			return err
		}
		if local != "" && op.Flow.MaxBytes > n.Limits.MaxTransferBytes {
			return errors.New("flow exceeds package transfer limit")
		}
	}
	return nil
}
func scalarSchema(s map[string]any, array bool) bool {
	types := []string{}
	switch v := s["type"].(type) {
	case string:
		types = []string{v}
	case []any:
		for _, t := range v {
			str, ok := t.(string)
			if !ok {
				return false
			}
			types = append(types, str)
		}
	}
	if len(types) == 0 {
		return false
	}
	for _, v := range types {
		switch v {
		case "string", "integer", "number", "boolean", "null":
		case "array":
			if !array {
				return false
			}
			items, ok := s["items"].(map[string]any)
			if !ok || !scalarSchema(items, false) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func validateBindings(bindings map[string]Binding, properties map[string]map[string]any, local string, jsonBody bool, auth string, form bool) error {
	for name, b := range bindings {
		if !parameterName.MatchString(name) || reservedField(name) {
			return errors.New("invalid binding field")
		}
		if b.ActionToken {
			if auth != "greader-client-login" || !form || name != "T" {
				return errors.New("actionToken only allowed as greader form T")
			}
			continue
		}
		if auth == "greader-client-login" && name == "T" {
			return errors.New("greader T is runtime-owned")
		}
		if b.Param != "" {
			s, ok := properties[b.Param]
			if !ok || b.Param == local {
				return errors.New("binding references unknown or local file parameter")
			}
			if !jsonBody && !scalarSchema(s, true) {
				return errors.New("query/form binding requires scalar or scalar array")
			}
		}
	}
	return nil
}
func validateResponse(r Response, max int64) error {
	if r.MaxBytes > max {
		return errors.New("response exceeds package limit")
	}
	if r.Decode == "binary" && r.Project != nil {
		return errors.New("binary response cannot project")
	}
	if r.Decode != "json" && r.Project != nil {
		return errors.New("only JSON response can project")
	}
	for _, ct := range r.ContentTypes {
		m, params, e := mime.ParseMediaType(ct)
		if e != nil || m != ct || len(params) > 0 || strings.Contains(m, "*") {
			return errors.New("response requires fixed MIME types without parameters")
		}
	}
	return nil
}
func validateFlow(o Operation) error {
	f, h := o.Flow, o.HTTP
	switch f.Type {
	case "invoke":
		if o.Kind != "rpc" || h.Response.Decode == "binary" {
			return errors.New("invoke requires rpc JSON/text")
		}
	case "paginate-http":
		if o.Kind != "rpc" || o.Effect != "read" || h.Method != "GET" || h.Response.Decode != "json" || h.Response.Project != nil {
			return errors.New("pagination requires unprojected GET JSON rpc")
		}
		p := f.Pagination
		first := p.PageParam
		if p.Mode == "cursor" {
			first = p.CursorParam
		}
		if !parameterName.MatchString(first) || !parameterName.MatchString(p.SizeParam) || first == p.SizeParam || reservedField(first) || reservedField(p.SizeParam) {
			return errors.New("invalid pagination parameter names")
		}
		if _, ok := h.Query[first]; ok {
			return errors.New("pagination parameter must be runtime-owned")
		}
		if _, ok := h.Query[p.SizeParam]; ok {
			return errors.New("pagination size must be runtime-owned")
		}
	case "ndjson":
		if o.Kind != "stream" || o.Effect != "read" || h.Method != "GET" || h.Response.Decode != "text" || h.Response.Project != nil {
			return errors.New("NDJSON requires bounded GET text stream")
		}
		if !contains(h.Response.ContentTypes, "application/x-ndjson") {
			return errors.New("NDJSON requires declared NDJSON MIME")
		}
	case "upload-http":
		if o.Kind != "stream" || o.AI != "human-only" || o.Effect == "read" || h.Method != "POST" || h.Response.MaxBytes > 65536 || h.Response.Decode == "binary" {
			return errors.New("upload requires human-only POST stream with bounded receipt")
		}
		if f.Encoding == "raw" && (h.Body != nil || f.Part != "") {
			return errors.New("raw upload cannot declare form fields or multipart part")
		}
		if f.Encoding == "multipart" && (!parameterName.MatchString(f.Part) || h.Body != nil && h.Body.Encoding != "form") {
			return errors.New("multipart requires a fixed part and optional form fields")
		}
		if h.Body != nil {
			if _, ok := h.Body.Fields[f.Part]; ok {
				return errors.New("multipart part collides with form field")
			}
		}
	case "download-file":
		if o.Kind != "stream" || o.AI != "human-only" || o.Effect != "read" || h.Method != "GET" || h.Response.Decode != "binary" || h.Response.MaxBytes > f.MaxBytes {
			return errors.New("download requires human-only GET binary stream")
		}
		for _, ct := range h.Response.ContentTypes {
			if !contains(f.ContentTypes, ct) {
				return errors.New("download MIME differs from flow")
			}
		}
	}
	for _, ct := range f.ContentTypes {
		m, params, e := mime.ParseMediaType(ct)
		if e != nil || ct != m || len(params) > 0 || strings.Contains(m, "*") {
			return errors.New("flow requires fixed MIME types")
		}
	}
	return nil
}
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
func validateOperationSchema(raw []byte) error {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	// Inspect only schema positions: const/default/examples are data, not schema.
	var scan func(any) error
	scan = func(v any) error {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		for k, val := range m {
			switch k {
			case "$id", "$schema":
				if k == "$schema" && val == "https://json-schema.org/draft/2020-12/schema" {
					continue
				}
				return errors.New("schema cannot change its resource or dialect")
			case "$ref", "$dynamicRef":
				s, ok := val.(string)
				if !ok || !strings.HasPrefix(s, "#") {
					return errors.New("only local schema references are allowed")
				}
			case "properties", "patternProperties", "$defs", "dependentSchemas":
				if props, ok := val.(map[string]any); ok {
					for _, s := range props {
						if e := scan(s); e != nil {
							return e
						}
					}
				}
			case "allOf", "anyOf", "oneOf", "prefixItems":
				if list, ok := val.([]any); ok {
					for _, s := range list {
						if e := scan(s); e != nil {
							return e
						}
					}
				}
			case "items", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "contains", "propertyNames", "not", "if", "then", "else":
				if e := scan(val); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if e := scan(doc); e != nil {
		return e
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(nil)
	if e := c.AddResource("https://synopkgland.invalid/operation-schema", doc); e != nil {
		return e
	}
	if _, e := c.Compile("https://synopkgland.invalid/operation-schema"); e != nil {
		return fmt.Errorf("invalid operation JSON Schema: %w", e)
	}
	// Keep the original lexical JSON for canonical digests.
	if _, e := Canonical(bytes.TrimSpace(raw)); e != nil {
		return e
	}
	return nil
}

// Resolve local property references for scalar/path binding checks. JSON bodies
// may still carry complex typed values through --input. Cyclic scalar refs fail.
func resolveSchema(s, root map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range s {
		out[k] = v
	}
	seen := map[string]bool{}
	for {
		ref, ok := out["$ref"].(string)
		if !ok {
			return out, nil
		}
		if seen[ref] {
			return nil, errors.New("cyclic scalar parameter schema reference")
		}
		seen[ref] = true
		fragment, e := url.PathUnescape(strings.TrimPrefix(ref, "#"))
		if e != nil || fragment != "" && !strings.HasPrefix(fragment, "/") {
			return nil, errors.New("scalar parameter reference requires a local JSON pointer")
		}
		var value any = root
		if fragment != "" {
			for _, segment := range strings.Split(fragment[1:], "/") {
				key := strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
				m, ok := value.(map[string]any)
				if !ok {
					return nil, errors.New("invalid scalar parameter schema reference")
				}
				value = m[key]
			}
		}
		target, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("invalid scalar parameter schema reference")
		}
		delete(out, "$ref")
		for k, v := range target {
			if _, exists := out[k]; !exists {
				out[k] = v
			}
		}
	}
}
