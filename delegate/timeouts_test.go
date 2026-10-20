package delegate

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// Timeouts is spec/protocol/v1/timeouts.json.
type Timeouts struct {
	Margin   int `json:"margin_s"`
	Executor struct {
		Op            int `json:"op_s"`
		RunMax        int `json:"root_run_program_max_s"`
		RootRunAnswer int `json:"root_run_answer_s"`
	} `json:"executor"`
	Client struct {
		Op      int `json:"op_s"`
		RootRun int `json:"root_run_s"`
	} `json:"client"`
	Request struct {
		Default int `json:"default_s"`
	} `json:"request"`
}

func specTimeouts(t *testing.T) Timeouts {
	t.Helper()
	b, err := os.ReadFile("../spec/protocol/v1/timeouts.json")
	if err != nil {
		t.Fatal(err)
	}
	var s Timeouts
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func seconds(n int) time.Duration { return time.Duration(n) * time.Second }

// Each layer waits a margin longer than the one it calls: the executor's
// answer reaches the client, the client's refusal reaches the request.
func TestEveryLayerWaitsPastTheLayerItCalls(t *testing.T) {
	s := specTimeouts(t)
	m := seconds(s.Margin)
	if ExecutorTimeout != seconds(s.Client.Op) || RootRunTimeout != seconds(s.Client.RootRun) || api.DefaultRequestTimeout != seconds(s.Request.Default) {
		t.Fatalf("SDK %v %v %v, spec %+v", ExecutorTimeout, RootRunTimeout, api.DefaultRequestTimeout, s)
	}
	for name, outer := range map[string][2]time.Duration{
		"root.run answer > program": {seconds(s.Executor.RootRunAnswer), seconds(s.Executor.RunMax)},
		"client > executor op":      {ExecutorTimeout, seconds(s.Executor.Op)},
		"client > root.run answer":  {RootRunTimeout, seconds(s.Executor.RootRunAnswer)},
		"request > client op":       {api.DefaultRequestTimeout, ExecutorTimeout},
	} {
		if outer[0] < outer[1]+m {
			t.Errorf("%s: %v does not wait %v past %v", name, outer[0], m, outer[1])
		}
	}
	if Local("p").Timeout != ExecutorTimeout {
		t.Fatal("Local waits", Local("p").Timeout)
	}
}

// an App's CGI client from Local read no lifecycle mailbox, so a
// stopped package answered EXECUTOR_UNAVAILABLE instead of APP_STOPPED.
func TestLocalReadsThePackageLifecycleMailbox(t *testing.T) {
	if got := Local("p").Mailbox; got != "/var/packages/p/var/executor" {
		t.Fatal("Local mailbox", got)
	}
}
