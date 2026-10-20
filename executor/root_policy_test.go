package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// rootDigests is the policy's root map in its shorthand form.
func rootDigests(m map[string]string) map[string]RootEntry {
	out := map[string]RootEntry{}
	for name, digest := range m {
		var entry RootEntry
		if err := json.Unmarshal([]byte(`"`+digest+`"`), &entry); err != nil {
			panic(err)
		}
		out[name] = entry
	}
	return out
}

var digestA = strings.Repeat("a", 64)

func rootPolicy(t *testing.T, root string) (Policy, error) {
	t.Helper()
	b := []byte(`{"v":1,"package":"Demo","app":"SYNO.SDS.Demo.Instance","operations":["status","root.run","root.start","root.jobs.list","root.jobs.get","root.jobs.stop"],"root":` + root + `}`)
	var p Policy
	if err := strictJSON(b, &p); err != nil {
		return p, err
	}
	return p, validatePolicy(p, "Demo")
}

func TestRootEntryShorthandIsAScriptRunWithTheDefaults(t *testing.T) {
	p, err := rootPolicy(t, `{"whoami":"`+digestA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	e := p.Root["whoami"]
	if e.Digest != digestA || e.Kind != "script" || e.Run == nil || e.Job != nil || e.Schema != nil {
		t.Fatalf("%+v", e)
	}
	if e.Run.timeout() != 20*time.Second || e.Run.maxOutput() != 1<<20 {
		t.Fatal(e.Run.timeout(), e.Run.maxOutput())
	}
}

func TestRootEntryDeclaresKindParamsTimeoutsAndJobs(t *testing.T) {
	p, err := rootPolicy(t, `{"cli":{"digest":"`+digestA+`","kind":"binary",
		"schema":{"type":"object","properties":{"volume":{"type":"string","pattern":"^/volume[0-9]+$"}},"required":["volume"],"additionalProperties":false},
		"run":{"timeout_s":120,"max_output":67108864},
		"job":{"timeout_s":604800,"grace_s":60,"max_concurrent":2,"nice":19}}}`)
	if err != nil {
		t.Fatal(err)
	}
	e := p.Root["cli"]
	if e.Kind != "binary" || e.Run.timeout() != 120*time.Second || e.Run.maxOutput() != 64<<20 {
		t.Fatalf("%+v", e)
	}
	if e.Job.timeout() != 7*24*time.Hour || e.Job.grace() != time.Minute || e.Job.concurrency() != 2 || e.Job.nice() != 19 {
		t.Fatalf("%+v", e.Job)
	}
	// A job with no declared limits gets a day, ten seconds of grace, one at a time and nice 10.
	p, err = rootPolicy(t, `{"scan":{"digest":"`+digestA+`","job":{}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if j := p.Root["scan"].Job; j.timeout() != 24*time.Hour || j.grace() != 10*time.Second || j.concurrency() != 1 || j.nice() != 10 || p.Root["scan"].Run != nil {
		t.Fatalf("%+v", j)
	}
}

func TestRootEntryRefusesWhatItCannotEnforce(t *testing.T) {
	for name, root := range map[string]string{
		"bad digest":              `{"x":"bad"}`,
		"unknown member":          `{"x":{"digest":"` + digestA + `","argv":["-rf"]}}`,
		"unknown kind":            `{"x":{"digest":"` + digestA + `","kind":"python"}}`,
		"run timeout above 120 s": `{"x":{"digest":"` + digestA + `","run":{"timeout_s":121}}}`,
		"run timeout zero":        `{"x":{"digest":"` + digestA + `","run":{"timeout_s":0}}}`,
		"output above 64 MiB":     `{"x":{"digest":"` + digestA + `","run":{"max_output":67108865}}}`,
		"job above 7 days":        `{"x":{"digest":"` + digestA + `","job":{"timeout_s":604801}}}`,
		"grace above 60 s":        `{"x":{"digest":"` + digestA + `","job":{"grace_s":61}}}`,
		"concurrency above 4":     `{"x":{"digest":"` + digestA + `","job":{"max_concurrent":5}}}`,
		"nice above 19":           `{"x":{"digest":"` + digestA + `","job":{"nice":20}}}`,
		"negative nice":           `{"x":{"digest":"` + digestA + `","job":{"nice":-1}}}`,
		"schema not an object":    `{"x":{"digest":"` + digestA + `","schema":{"type":"array","items":{"type":"string"}}}}`,
		"open object":             `{"x":{"digest":"` + digestA + `","schema":{"type":"object","properties":{}}}}`,
		"open nested object":      `{"x":{"digest":"` + digestA + `","schema":{"type":"object","properties":{"o":{"type":"object","properties":{}}},"additionalProperties":false}}}`,
		"unknown keyword":         `{"x":{"digest":"` + digestA + `","schema":{"type":"object","properties":{"a":{"type":"string","format":"uri"}},"additionalProperties":false}}}`,
		"unknown type":            `{"x":{"digest":"` + digestA + `","schema":{"type":"object","properties":{"a":{"type":"file"}},"additionalProperties":false}}}`,
		"bad pattern":             `{"x":{"digest":"` + digestA + `","schema":{"type":"object","properties":{"a":{"type":"string","pattern":"("}},"additionalProperties":false}}}`,
		"noncanonical key":        `{"x":{"digest":"` + digestA + `","schema":{"type":"object","properties":{"Volume":{"type":"string"}},"additionalProperties":false}}}`,
		"required unknown":        `{"x":{"digest":"` + digestA + `","schema":{"type":"object","properties":{},"required":["a"],"additionalProperties":false}}}`,
		"array without items":     `{"x":{"digest":"` + digestA + `","schema":{"type":"object","properties":{"a":{"type":"array"}},"additionalProperties":false}}}`,
		"keyword of another type": `{"x":{"digest":"` + digestA + `","schema":{"type":"object","properties":{"a":{"type":"integer","pattern":"^1$"}},"additionalProperties":false}}}`,
	} {
		if _, err := rootPolicy(t, root); err == nil {
			t.Error(name, "accepted")
		}
	}
}

func TestRootParamsFollowTheDeclaredSchema(t *testing.T) {
	p, err := rootPolicy(t, `{"cli":{"digest":"`+digestA+`","schema":{"type":"object","additionalProperties":false,"required":["method"],"properties":{
		"method":{"type":"string","enum":["scan","doctor"]},
		"volume":{"type":"string","pattern":"^/volume[0-9]+$","maxLength":16},
		"paths":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":2},
		"limit":{"type":"integer","minimum":1,"maximum":200},
		"ratio":{"type":"number","minimum":0,"maximum":1},
		"snapshots":{"type":"boolean"},
		"expect":{"type":"object","additionalProperties":false,"properties":{"bytes":{"type":"integer"}}}}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	schema := p.Root["cli"].Schema
	for _, good := range []string{
		`{"method":"scan"}`,
		`{"method":"doctor","volume":"/volume1","paths":["/volume1/a","/volume1/b"],"limit":200,"ratio":0.5,"snapshots":true,"expect":{"bytes":3}}`,
	} {
		if err := schema.check([]byte(good)); err != nil {
			t.Errorf("%s: %v", good, err)
		}
	}
	for _, bad := range []string{
		`{}`,
		`{"method":"rm"}`,
		`{"method":"scan","other":1}`,
		`{"method":"scan","volume":"/etc"}`,
		`{"method":"scan","volume":"/volume12345678901"}`,
		`{"method":"scan","paths":[]}`,
		`{"method":"scan","paths":["a","b","c"]}`,
		`{"method":"scan","paths":[""]}`,
		`{"method":"scan","paths":[1]}`,
		`{"method":"scan","limit":0}`,
		`{"method":"scan","limit":1.5}`,
		`{"method":"scan","limit":"1"}`,
		`{"method":"scan","ratio":2}`,
		`{"method":"scan","snapshots":"yes"}`,
		`{"method":"scan","expect":{"bytes":1,"more":2}}`,
		`{"method":"scan","volume":null}`,
		`[]`,
	} {
		if err := schema.check([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	// Without a schema an op takes no params.
	var none *paramSchema
	if none.check([]byte(`{}`)) != nil || none.check([]byte(`{"a":1}`)) == nil {
		t.Fatal("an op without a schema takes params")
	}
}
