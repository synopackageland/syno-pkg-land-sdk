package main

import (
	"os"
	"strings"
	"testing"
)

// Reclaim's approved policy declares a root param named params; its schema
// keywords are camelCase and must not be held to request key rules.
func TestPolicyWithAParamsSchemaLoads(t *testing.T) {
	b, err := os.ReadFile("testdata/policy-reclaim-lifecycle.json")
	if err != nil {
		t.Fatal(err)
	}
	var p Policy
	if err := strictJSON(b, &p); err != nil {
		t.Fatal(err)
	}
	if err := validatePolicy(p, "SynoReclaim"); err != nil {
		t.Fatal(err)
	}
}

// The request key rules apply to a request's own auth and params only; a
// policy may name a root program or a schema property auth or params.
func TestPolicyMayNameAuthOrParamsAnywhere(t *testing.T) {
	schema := func(property string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"` + property + `":{"type":"string","maxLength":8}}}`
	}
	for name, root := range map[string]string{
		"schema property auth":     `{"cli":{"digest":"` + digestA + `","schema":` + schema("auth") + `}}`,
		"root program params":      `{"params":{"digest":"` + digestA + `","schema":` + schema("volume") + `}}`,
		"root program Auth":        `{"Auth":{"digest":"` + digestA + `","schema":` + schema("volume") + `}}`,
		"root program PARAMS form": `{"PARAMS":"` + digestA + `"}`,
	} {
		if _, err := rootPolicy(t, root); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// check-policy validates a policy on stdin by the rules load() applies, so a
// build can refuse a policy the executor would refuse. It needs no root.
func TestCheckPolicyCommand(t *testing.T) {
	valid, err := os.ReadFile("testdata/policy-reclaim-lifecycle.json")
	if err != nil {
		t.Fatal(err)
	}
	stopIsCli, err := os.ReadFile("testdata/policy-reclaim.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		pkg   string
		input []byte
		ok    bool
	}{
		"approved policy":  {"SynoReclaim", valid, true},
		"stop is cli":      {"SynoReclaim", stopIsCli, false},
		"another package":  {"Other", valid, false},
		"not JSON":         {"SynoReclaim", []byte("{"), false},
		"unknown member":   {"SynoReclaim", []byte(`{"v":1,"package":"SynoReclaim","app":"A","operations":["status"],"extra":1}`), false},
		"above the 64 KiB": {"SynoReclaim", append(valid[:len(valid)-1:len(valid)-1], []byte(`,"lifetime":"`+strings.Repeat(" ", maxRequest)+`"}`)...), false},
	} {
		stdin, err := os.CreateTemp(t.TempDir(), "policy")
		if err != nil {
			t.Fatal(err)
		}
		stdin.Write(c.input)
		stdin.Seek(0, 0)
		saved := os.Stdin
		os.Stdin = stdin
		err = run([]string{"check-policy", "--package", c.pkg})
		os.Stdin = saved
		stdin.Close()
		if (err == nil) != c.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
	if run([]string{"check-policy", "--package", "../x"}) == nil {
		t.Error("invalid package accepted")
	}
}

// Reclaim 1556 names its session program cli as its stop and purge steps.
// The executor refuses sessions a lifecycle step, so a check that passed it
// let every root call of the approved App end in POLICY_DENIED.
func TestAStopOrPurgeStepIsNoSessionProgram(t *testing.T) {
	b, err := os.ReadFile("testdata/policy-reclaim.json")
	if err != nil {
		t.Fatal(err)
	}
	var p Policy
	if err := strictJSON(b, &p); err != nil {
		t.Fatal(err)
	}
	err = validatePolicy(p, "SynoReclaim")
	if err == nil || !strings.Contains(err.Error(), "policy stop") || !strings.Contains(err.Error(), `"cli"`) {
		t.Fatal(err)
	}
	p.Stop = ""
	if err = validatePolicy(p, "SynoReclaim"); err == nil || !strings.Contains(err.Error(), "policy purge") || !strings.Contains(err.Error(), `"cli"`) {
		t.Fatal(err)
	}
	for name, root := range map[string]string{
		"stop with run":  `{"stop":{"digest":"` + digestA + `","run":{}}}`,
		"stop with job":  `{"stop":{"digest":"` + digestA + `","job":{}}}`,
		"purge with run": `{"purge":{"digest":"` + digestA + `","run":{}}}`,
	} {
		p, err := rootPolicy(t, root)
		if err != nil {
			t.Fatal(name, err)
		}
		if name == "purge with run" {
			p.Purge = "purge"
		} else {
			p.Stop = "stop"
		}
		if validatePolicy(p, "Demo") == nil {
			t.Error(name, "accepted")
		}
	}
}

// A step declared with neither run nor job, or as the shorthand digest, is
// only a lifecycle step; the session programs keep their own names.
func TestALifecycleOnlyStepPasses(t *testing.T) {
	b, err := os.ReadFile("testdata/policy-reclaim-lifecycle.json")
	if err != nil {
		t.Fatal(err)
	}
	var p Policy
	if err := strictJSON(b, &p); err != nil {
		t.Fatal(err)
	}
	if err := validatePolicy(p, "SynoReclaim"); err != nil {
		t.Fatal(err)
	}
	p, err = rootPolicy(t, `{"whoami":"`+digestA+`","purge":"`+strings.Repeat("b", 64)+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	p.Purge, p.Stop = "purge", "purge"
	if err := validatePolicy(p, "Demo"); err != nil {
		t.Fatal(err)
	}
}

// Every files op acts through a delegation, and only delegate.create makes
// one: without it the executor would deny each files call.
func TestFilesOpsNeedDelegateCreate(t *testing.T) {
	for ops, ok := range map[string]bool{
		`"status","delegate.create","files.list"`:    true,
		`"status","files.list"`:                      false,
		`"status","delegate.list","files.read"`:      false,
		`"status","delegate.list","delegate.revoke"`: true,
	} {
		var p Policy
		if err := strictJSON([]byte(`{"v":1,"package":"Demo","app":"SYNO.SDS.Demo.Instance","scope":"home","operations":[`+ops+`]}`), &p); err != nil {
			t.Fatal(err)
		}
		if err := validatePolicy(p, "Demo"); (err == nil) != ok {
			t.Error(ops, err)
		}
	}
}
