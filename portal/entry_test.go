package portal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// executor answers ops like the executor's socket; calls records them.
type executor struct {
	replies map[string]string
	err     error
	calls   []string
	auth    map[string]string
}

func (e *executor) Call(_ context.Context, op string, auth map[string]string, params, result any) error {
	e.calls = append(e.calls, op)
	e.auth = auth
	if b, _ := json.Marshal(params); params != nil && string(b) != "{}" {
		return errors.New("unexpected params " + string(b))
	}
	if e.err != nil {
		return e.err
	}
	return json.Unmarshal([]byte(e.replies[op]), result)
}

var admin = map[string]string{"dsm_cookie": "id", "dsm_synotoken": "token", "remote_addr": "192.0.2.1"}

func TestRegisterAndUnregisterAnswerTheEntry(t *testing.T) {
	x := &executor{replies: map[string]string{"portal.register": `{"entry":"registered"}`, "portal.unregister": `{"entry":"unregistered"}`}}
	if got, err := Register(context.Background(), x, admin); err != nil || got != EntryRegistered {
		t.Fatal(got, err)
	}
	if x.auth["dsm_cookie"] != "id" {
		t.Fatal("no session sent")
	}
	if got, err := Unregister(context.Background(), x, admin); err != nil || got != EntryUnregistered {
		t.Fatal(got, err)
	}
}

func TestExecutorRefusalsPassThrough(t *testing.T) {
	refused := errors.New("PORTAL_FAILED")
	x := &executor{err: refused}
	if _, err := Register(context.Background(), x, admin); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if _, err := Unregister(context.Background(), x, admin); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if _, err := ExecutorEntry(context.Background(), x); !errors.Is(err, refused) {
		t.Fatal(err)
	}
}

func TestExecutorEntryReadsStatus(t *testing.T) {
	x := &executor{replies: map[string]string{"status": `{"version":"1","ready":true,"portal":"cleanup-required"}`}}
	got, err := ExecutorEntry(context.Background(), x)
	if err != nil || got != EntryCleanupRequired || x.calls[0] != "status" || x.auth != nil {
		t.Fatal(got, err, x.calls, x.auth)
	}
}

func TestAnUnknownEntryIsAnError(t *testing.T) {
	for _, reply := range []string{`{}`, `{"portal":"maybe"}`, `{"portal":1}`} {
		x := &executor{replies: map[string]string{"status": reply, "portal.register": `{"entry":"maybe"}`}}
		if _, err := ExecutorEntry(context.Background(), x); err == nil {
			t.Fatal(reply)
		}
	}
	x := &executor{replies: map[string]string{"portal.register": `{"entry":"maybe"}`}}
	if _, err := Register(context.Background(), x, admin); err == nil {
		t.Fatal("unknown entry")
	}
}

func TestWithEntry(t *testing.T) {
	active := Status{State: Active, Alias: "demo"}
	for _, tc := range []struct {
		entry string
		want  Status
	}{
		{EntryRegistered, Status{State: Active, Alias: "demo", Entry: EntryRegistered}},
		// Without the app.d entry DSM serves no alias, whatever it saved.
		{EntryUnregistered, Status{State: Inactive, Entry: EntryUnregistered}},
		// A partial entry may still answer at the alias.
		{EntryCleanupRequired, Status{State: Active, Alias: "demo", Entry: EntryCleanupRequired}},
		{"", Status{State: Unavailable}},
		{"maybe", Status{State: Unavailable}},
	} {
		if got := WithEntry(active, tc.entry); got != tc.want {
			t.Errorf("%q: %+v", tc.entry, got)
		}
	}
}
