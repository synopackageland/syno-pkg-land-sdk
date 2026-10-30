package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// The executor's bounds are spec/protocol/v1/timeouts.json, which its
// clients wait past.
func TestExecutorBoundsAreTheSpecTimeouts(t *testing.T) {
	b, err := os.ReadFile("../spec/protocol/v1/timeouts.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Executor struct {
			Op            int `json:"op_s"`
			RawExecution  int `json:"webapi_raw_execution_s"`
			RawAnswer     int `json:"webapi_raw_answer_s"`
			RunDefault    int `json:"root_run_program_default_s"`
			RunMax        int `json:"root_run_program_max_s"`
			RootRunAnswer int `json:"root_run_answer_s"`
			StartStep     int `json:"start_step_s"`
		} `json:"executor"`
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	s := func(n int) time.Duration { return time.Duration(n) * time.Second }
	e := spec.Executor
	for name, pair := range map[string][2]time.Duration{
		"op_s":                       {executorOpBound, s(e.Op)},
		"webapi_raw_execution_s":     {webAPIRawTimeout, s(e.RawExecution)},
		"webapi_raw_answer_s":        {webAPIRawTimeout + 10*time.Second, s(e.RawAnswer)},
		"root_run_program_default_s": {defaultRootRunTimeout, s(e.RunDefault)},
		"root_run_program_max_s":     {maxRootRunTimeout, s(e.RunMax)},
		"root_run_answer_s":          {rootRunRequestBy, s(e.RootRunAnswer)},
		"start_step_s":               {startStepTimeout, s(e.StartStep)},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: executor %v, spec %v", name, pair[0], pair[1])
		}
	}
}
