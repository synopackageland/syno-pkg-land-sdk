package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/taskscript"
)

// readbackVectors is spec/scheduler/v1/readback.json: the verdict of one
// scheduler readback, shared with the CLI's DSM window approval.
type readbackVectors struct {
	Package              string `json:"package"`
	PolicyDigest         string `json:"policyDigest"`
	OutdatedPolicyDigest string `json:"outdatedPolicyDigest"`
	Cases                []struct {
		Name   string          `json:"name"`
		Kind   string          `json:"kind"`
		List   json.RawMessage `json:"list"`
		Get    json.RawMessage `json:"get"`
		Expect string          `json:"expect"`
	} `json:"cases"`
}

// vectorScheduler answers TaskScheduler list with list and every get with get.
type vectorScheduler struct{ list, get string }

func (v vectorScheduler) Exec(_ context.Context, _ string, args []string) ([]byte, error) {
	data := v.get
	if contains(args, "method=list") {
		data = `{"tasks":` + v.list + `,"total":` + itoa(len(mustTasks(v.list))) + `}`
	}
	return []byte(`{"success":true,"data":` + data + `}`), nil
}

func mustTasks(list string) []json.RawMessage {
	var tasks []json.RawMessage
	json.Unmarshal([]byte(list), &tasks)
	return tasks
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// The executor accepts a task exactly when the shared vectors expect ready.
func TestSchedulerReadbackVectors(t *testing.T) {
	b, err := os.ReadFile("../spec/scheduler/v1/readback.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors readbackVectors
	if err = json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	current, err := taskscript.Generate(vectors.Package, vectors.PolicyDigest)
	if err != nil {
		t.Fatal(err)
	}
	outdated, err := taskscript.Generate(vectors.Package, vectors.OutdatedPolicyDigest)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range vectors.Cases {
		t.Run(c.Name, func(t *testing.T) {
			i := map[string]int{"boot": 0, "control": 1}[c.Kind]
			task := current[i]
			quote := func(s string) string { q, _ := json.Marshal(s); return string(q[1 : len(q)-1]) }
			fill := func(raw json.RawMessage) string {
				s := strings.ReplaceAll(string(raw), "@NAME@", quote(task.Name))
				s = strings.ReplaceAll(s, "@OUTDATED@", quote(outdated[i].Script))
				return strings.ReplaceAll(s, "@SCRIPT@", quote(task.Script))
			}
			get := fill(c.Get)
			if len(c.Get) == 0 {
				get = "null"
			}
			list := fill(c.List)
			if len(c.List) == 0 {
				list = "[]"
			}
			approved := approvedTask{Kind: task.Kind, Name: task.Name, ScriptDigest: hashBytes([]byte(task.Script))}
			_, _, _, err := taskReference(context.Background(), &liveDSM{Command: vectorScheduler{list: list, get: get}}, approved, task.Script, false)
			if (err == nil) != (c.Expect == "ready") {
				t.Fatalf("expect %s, got error %v", c.Expect, err)
			}
		})
	}
}
