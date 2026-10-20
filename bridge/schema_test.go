package bridge

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type schemaDef struct {
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
}

func frameSchema(t *testing.T) map[string]schemaDef {
	raw, err := os.ReadFile("../spec/bridge/v1/frames.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Defs map[string]schemaDef `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s.Defs
}

func checkKeys(t *testing.T, def schemaDef, line []byte) {
	t.Helper()
	var m map[string]json.RawMessage
	json.Unmarshal(line, &m)
	for k := range m {
		if _, ok := def.Properties[k]; !ok {
			t.Errorf("%s: %q is not in the schema", line, k)
		}
	}
	for _, k := range def.Required {
		if _, ok := m[k]; !ok {
			t.Errorf("%s: required %q missing", line, k)
		}
	}
}

// Every frame the runtime sends fits its definition in spec/bridge/v1.
func TestRuntimeFramesMatchTheSchema(t *testing.T) {
	defs := frameSchema(t)
	sent := map[string][]byte{}
	app := fakeApp{methods: []string{"a.b"}, handle: func(c *fakeConn, _ map[string]any) map[string]any {
		sent["runtimeHello"], sent["call"] = c.seen[0], c.last
		c.svc("x.y", nil)
		sent["svcResult"] = c.last
		c.recv()
		sent["cancel"] = c.last
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s := &Session{Dial: pipeDialer(app), Methods: []string{"a.b"}}
	s.Call(ctx, "a.b", nil, &Call{Context: alice})
	for _, name := range []string{"runtimeHello", "call", "svcResult", "cancel"} {
		if sent[name] == nil {
			t.Fatalf("no %s frame", name)
		}
		checkKeys(t, defs[name], sent[name])
	}
}

// The runtime reads exactly the members the App's frames may carry.
func TestRuntimeDecoderKnowsTheAppFrames(t *testing.T) {
	defs := frameSchema(t)
	var known []string
	ft := reflect.TypeFor[frame]()
	for i := range ft.NumField() {
		name, _, _ := strings.Cut(ft.Field(i).Tag.Get("json"), ",")
		known = append(known, name)
	}
	var want []string
	for _, name := range []string{"appHello", "svc", "result"} {
		for k := range defs[name].Properties {
			if !slices.Contains(want, k) {
				want = append(want, k)
			}
		}
	}
	slices.Sort(known)
	slices.Sort(want)
	if !slices.Equal(known, want) {
		t.Fatalf("decoder %v, schema %v", known, want)
	}
}
