package bridge

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const validManifest = `{
  "schema": 1,
  "runtime": "node",
  "entry": "app/main.mjs",
  "mode": "exec",
  "methods": ["hello.greet", "hello.save"],
  "access": "admin-only",
  "timeout_seconds": 60,
  "max_request_bytes": 1048576,
  "env": {"APP_MODE": "production"},
  "node": {"package": "Node.js_v20"},
  "transfers": {"hello.save": "upload"},
  "max_upload_bytes": 1048576,
  "capabilities": {"files": "session", "identity": "standalone", "portal": true, "jobs": true, "root": true,
    "delegate": {"scope": "home", "destinations": ["save"], "executor_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "policy_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
}`

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Runtime != "node" || m.Entry != "app/main.mjs" || len(m.Methods) != 2 || m.Access != "admin-only" ||
		m.TimeoutSeconds != 60 || m.MaxRequestBytes != 1<<20 || m.Env["APP_MODE"] != "production" ||
		m.Node.Package != "Node.js_v20" || m.Capabilities.Files != "session" || m.Capabilities.Identity != "standalone" || !m.Capabilities.Portal ||
		m.Transfers["hello.save"] != "upload" || m.MaxUploadBytes != 1<<20 || !m.Capabilities.Jobs || !m.Capabilities.Root ||
		m.Capabilities.Delegate.Scope != "home" || m.Capabilities.Delegate.Destinations[0] != "save" {
		t.Fatalf("manifest %+v", m)
	}
}

func TestParseManifestRefusesWhatTheRuntimeCannotHonour(t *testing.T) {
	for name, edit := range map[string][2]string{
		"schema":            {`"schema": 1`, `"schema": 2`},
		"runtime":           {`"runtime": "node"`, `"runtime": "ruby"`},
		"socket mode":       {`"mode": "exec"`, `"mode": "socket"`},
		"no mode":           {`"mode": "exec",`, ``},
		"absolute entry":    {`"app/main.mjs"`, `"/bin/sh"`},
		"entry outside":     {`"app/main.mjs"`, `"../x.mjs"`},
		"no methods":        {`["hello.greet", "hello.save"]`, `[]`},
		"method name":       {`"hello.save"`, `"Hello"`},
		"long method":       {`"hello.save"`, `"hello.` + strings.Repeat("s", 123) + `"`},
		"duplicate method":  {`"hello.save"`, `"hello.greet"`},
		"access":            {`"admin-only"`, `"everyone"`},
		"timeout":           {`"timeout_seconds": 60`, `"timeout_seconds": -1`},
		"long timeout":      {`"timeout_seconds": 60`, `"timeout_seconds": 3601`},
		"request bytes":     {`"max_request_bytes": 1048576`, `"max_request_bytes": 99999999999`},
		"env name":          {`"APP_MODE"`, `"app mode"`},
		"env cookie":        {`"APP_MODE"`, `"HTTP_COOKIE"`},
		"env runtime":       {`"APP_MODE"`, `"SYNOPKGLAND_DATA_DIR"`},
		"env loader":        {`"APP_MODE"`, `"LD_PRELOAD"`},
		"env path":          {`"APP_MODE"`, `"PATH"`},
		"env nul":           {`"production"`, `"a\u0000b"`},
		"node package":      {`"Node.js_v20"`, `"../../bin"`},
		"node without node": {`"node": {"package": "Node.js_v20"},`, ``},
		"files":             {`"files": "session"`, `"files": "root"`},
		"identity":          {`"identity": "standalone"`, `"identity": "oauth"`},
		"unknown field":     {`"schema": 1`, `"schema": 1, "transfer": {}`},
		"sdk method":        {`"hello.save"`, `"delegate.create"`},
		// capabilities.root serves these itself: the App cannot report the executor's pins.
		"executor status":   {`"hello.save"`, `"executor.status"`},
		"executor control":  {`"hello.save"`, `"executor.control"`},
		"transfer kind":     {`"hello.save": "upload"`, `"hello.save": "stream"`},
		"transfer method":   {`"hello.save": "upload"`, `"hello.other": "upload"`},
		"upload bytes":      {`"max_upload_bytes": 1048576`, `"max_upload_bytes": 17179869185`},
		"delegate scope":    {`"scope": "home"`, `"scope": "/volume1"`},
		"no destinations":   {`"destinations": ["save"]`, `"destinations": []`},
		"destination key":   {`"destinations": ["save"]`, `"destinations": ["Save"]`},
		"twice destination": {`"destinations": ["save"]`, `"destinations": ["save", "save"]`},
		"executor digest":   {`"executor_digest": "a`, `"executor_digest": "A`},
		"no policy digest":  {`, "policy_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, ``},
	} {
		raw := strings.Replace(validManifest, edit[0], edit[1], 1)
		if raw == validManifest {
			t.Fatalf("%s: edit did not apply", name)
		}
		if _, err := ParseManifest([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseManifestOptionalFields(t *testing.T) {
	m, err := ParseManifest([]byte(`{"schema":1,"runtime":"python","entry":"app/main.py","mode":"exec","methods":["a.b"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Access != "" || m.TimeoutSeconds != 0 || m.Env != nil || m.Node != nil {
		t.Fatalf("manifest %+v", m)
	}
	if _, err := ParseManifest([]byte(`{"schema":1,"runtime":"python","entry":"app/main.py","mode":"exec","methods":["a.b"],"node":{"package":"Node.js_v20"}}`)); err == nil {
		t.Fatal("a python App with a node package was accepted")
	}
}

// Manifest reads exactly the members spec/bridge/v1/manifest.schema.json defines.
func TestManifestMatchesTheSchema(t *testing.T) {
	raw, err := os.ReadFile("../spec/bridge/v1/manifest.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	type object struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	var schema object
	json.Unmarshal(raw, &schema)
	var sub map[string]object
	json.Unmarshal(raw, &struct {
		Properties *map[string]object `json:"properties"`
	}{&sub})
	var caps, delegate object
	json.Unmarshal(schema.Properties["capabilities"], &caps)
	json.Unmarshal(caps.Properties["delegate"], &delegate)
	for _, c := range []struct {
		name   string
		schema map[string]json.RawMessage
		typ    reflect.Type
	}{
		{"manifest", schema.Properties, reflect.TypeFor[Manifest]()},
		{"node", sub["node"].Properties, reflect.TypeFor[NodeRuntime]()},
		{"capabilities", sub["capabilities"].Properties, reflect.TypeFor[Capabilities]()},
		{"delegate", delegate.Properties, reflect.TypeFor[DelegateCapability]()},
	} {
		var fields, members []string
		for i := range c.typ.NumField() {
			name, _, _ := strings.Cut(c.typ.Field(i).Tag.Get("json"), ",")
			fields = append(fields, name)
		}
		for k := range c.schema {
			members = append(members, k)
		}
		slices.Sort(fields)
		slices.Sort(members)
		if !slices.Equal(fields, members) {
			t.Errorf("%s: Go %v, schema %v", c.name, fields, members)
		}
	}
}
