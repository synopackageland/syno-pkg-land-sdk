package notify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func notifier(t *testing.T) Notifier {
	t.Helper()
	return Notifier{
		Events:   map[string]Event{"device.offline": {Vars: []string{"device"}}},
		Recorder: FileRecorder{Dir: t.TempDir()},
	}
}

func TestAdmissionPersistsIdentityAndThrottlesOnlyEventKey(t *testing.T) {
	n := notifier(t)
	ctx := context.Background()
	vars := map[string]string{"device": "Example device"}
	first, err := n.Notify(ctx, "device.offline", vars, Options{Key: "incident:open"})
	if err != nil || first.DeliveryID == "" || first.Throttled {
		t.Fatalf("%+v %v", first, err)
	}
	// A new instance reads the log, including the immutable retry payload.
	n.Recorder = FileRecorder{Dir: n.Recorder.(FileRecorder).Dir}
	repeat, err := n.Notify(ctx, "device.offline", vars, Options{Key: "incident:open"})
	if err != nil || repeat.DeliveryID != first.DeliveryID || !repeat.Throttled {
		t.Fatalf("%+v %v", repeat, err)
	}
	retry, err := n.Notify(ctx, "device.offline", vars, Options{Key: "incident:open", DeliveryID: first.DeliveryID})
	if err != nil || retry.DeliveryID != first.DeliveryID || retry.Throttled {
		t.Fatalf("%+v %v", retry, err)
	}
	for _, key := range []string{"incident:resolved", "incident:escalated", "another:open"} {
		next, err := n.Notify(ctx, "device.offline", vars, Options{Key: key})
		if err != nil || next.Throttled || next.DeliveryID == first.DeliveryID {
			t.Fatalf("%s: %+v %v", key, next, err)
		}
	}
	_, err = n.Notify(ctx, "device.offline", map[string]string{"device": "Changed"}, Options{Key: "incident:open", DeliveryID: first.DeliveryID})
	if err == nil {
		t.Fatal("accepted changed content under a persisted identity")
	}
}

func TestConcurrentAdmissionsShareOneIdentity(t *testing.T) {
	n := notifier(t)
	var wg sync.WaitGroup
	results := make(chan Result, 16)
	for range 16 {
		wg.Go(func() {
			result, err := n.Notify(context.Background(), "device.offline", map[string]string{"device": "Example"}, Options{Key: "same"})
			if err != nil {
				t.Error(err)
			}
			results <- result
		})
	}
	wg.Wait()
	close(results)
	id, fresh := "", 0
	for result := range results {
		if id == "" {
			id = result.DeliveryID
		}
		if result.DeliveryID != id {
			t.Fatal("concurrent admissions got different IDs")
		}
		if !result.Throttled {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("%d admissions", fresh)
	}
}

func TestValidationAndRecorderFailuresNeverSend(t *testing.T) {
	n := notifier(t)
	for _, vars := range []map[string]string{nil, {"extra": "value"}, {"device": "Example", "extra": "value"}} {
		if _, err := n.Notify(context.Background(), "device.offline", vars, Options{}); err == nil {
			t.Fatal("accepted invalid vars")
		}
	}
	if _, err := n.Notify(context.Background(), "unknown", nil, Options{}); err == nil {
		t.Fatal("accepted unknown event")
	}
	if _, err := n.Notify(context.Background(), "device.offline", map[string]string{"device": "Example"}, Options{DeliveryID: "bad\r\nID"}); err == nil {
		t.Fatal("accepted header injection")
	}
	log := filepath.Join(n.Recorder.(FileRecorder).Dir, "events.json")
	if err := os.WriteFile(log, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Notify(context.Background(), "device.offline", map[string]string{"device": "Example"}, Options{}); err == nil {
		t.Fatal("accepted corrupt event log")
	}
}

func TestThrottleExpiryDefaultsAndCancellation(t *testing.T) {
	n := notifier(t)
	vars := map[string]string{"device": "Example"}
	result, err := n.Notify(context.Background(), "device.offline", vars, Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(n.Recorder.(FileRecorder).Dir, "events.json")
	data, _ := os.ReadFile(path)
	entries := map[string]loggedRecord{}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	entry := entries[result.DeliveryID]
	if len(entry.Record.Users) != 1 || entry.Record.Users[0] != "@administrators" {
		t.Fatal(entry)
	}
	entry.At = time.Now().Add(-11 * time.Minute)
	entries[result.DeliveryID] = entry
	data, _ = json.Marshal(entries)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := n.Notify(context.Background(), "device.offline", vars, Options{})
	if err != nil || next.Throttled || next.DeliveryID == result.DeliveryID {
		t.Fatalf("%+v %v", next, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.Notify(ctx, "device.offline", vars, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
