package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func auditEvent(n int) AuditEvent {
	return AuditEvent{At: time.Unix(int64(n), 0).UTC(), Package: "example", Username: "alice", UID: 1000, Operation: "webapi.call", Path: "calendar:SYNO.Cal.Event/list/6", Result: fmt.Sprint(n)}
}

func auditEvents(t *testing.T, s *Store) []AuditEvent {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.Dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []AuditEvent
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var event AuditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func TestReadAuditFullBufferNeverTouchesDisk(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			s := &Store{Dir: t.TempDir()}
			path := filepath.Join(s.Dir, "audit.jsonl")
			var before os.FileInfo
			var original []byte
			if existing {
				if err := s.Audit(auditEvent(-1)); err != nil {
					t.Fatal(err)
				}
				before, _ = os.Stat(path)
				original, _ = os.ReadFile(path)
			}
			for n := 0; n < 1000; n++ {
				if err := s.AuditReadOnly(auditEvent(n)); err != nil {
					t.Fatal(err)
				}
			}
			if s.audit.bytes > maxBufferedAuditBytes || len(s.audit.events) > maxBufferedAuditEvents {
				t.Fatal("unbounded buffer")
			}
			if existing {
				after, _ := os.Stat(path)
				content, _ := os.ReadFile(path)
				if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || string(content) != string(original) {
					t.Fatal("ring overflow changed audit file")
				}
			} else if entries, err := os.ReadDir(s.Dir); err != nil || len(entries) != 0 {
				t.Fatalf("read audit touched protected state: %v %v", entries, err)
			}
			retained := len(s.audit.events)
			if err := s.FlushAudit(); err != nil {
				t.Fatal(err)
			}
			events := auditEvents(t, s)
			start := 0
			if existing {
				start++
			}
			if events[start].Result != fmt.Sprintf("dropped:%d", 1000-retained) {
				t.Fatalf("missing single drop marker: %+v", events[start])
			}
			if len(events) != start+retained+1 {
				t.Fatal("incorrect marker/event count")
			}
			for i, event := range events[start+1:] {
				if event.Result != fmt.Sprint(1000-retained+i) {
					t.Fatal("ring did not keep newest events in order")
				}
			}
			if err := s.FlushAudit(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(events, auditEvents(t, s)) {
				t.Fatal("empty flush duplicated events")
			}
		})
	}
}

func TestReadAuditByteLimitAndOversize(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	for i := 0; i < 100; i++ {
		event := auditEvent(i)
		event.Path = strings.Repeat("x", 8192)
		if err := s.AuditReadOnly(event); err != nil {
			t.Fatal(err)
		}
	}
	large := auditEvent(100)
	large.Path = strings.Repeat("x", maxBufferedAuditBytes+1)
	if err := s.AuditReadOnly(large); err != nil {
		t.Fatal(err)
	}
	if s.audit.bytes > maxBufferedAuditBytes || s.audit.dropped == 0 {
		t.Fatal("byte cap did not drop events")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "audit.jsonl")); !os.IsNotExist(err) {
		t.Fatal("oversize read audit touched disk")
	}
	if err := s.Audit(auditEvent(101)); err != nil {
		t.Fatal(err)
	}
	events := auditEvents(t, s)
	if !strings.HasPrefix(events[0].Result, "dropped:") || events[len(events)-1].Result != "101" {
		t.Fatal("drop marker or mutation ordering")
	}
}

func TestReadAuditAwakeMutationAndFailedFlush(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if err := s.AuditReadOnly(auditEvent(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.FlushAuditIfAwake(func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "audit.jsonl")); !os.IsNotExist(err) {
		t.Fatal("asleep check wrote audit")
	}
	path := filepath.Join(s.Dir, "audit.jsonl")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.FlushAuditIfAwake(func() bool { return true }); err == nil {
		t.Fatal("invalid audit destination accepted")
	}
	if len(s.audit.events) != 1 {
		t.Fatal("failed flush lost pending event")
	}
	if err := s.Audit(auditEvent(2)); err == nil {
		t.Fatal("mutation must fail closed on storage error")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := s.FlushAuditIfAwake(func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if err := s.AuditReadOnly(auditEvent(3)); err != nil {
		t.Fatal(err)
	}
	if err := s.Audit(auditEvent(4)); err != nil {
		t.Fatal(err)
	}
	events := auditEvents(t, s)
	var got []string
	for _, e := range events {
		got = append(got, e.Result)
	}
	if !reflect.DeepEqual(got, []string{"1", "3", "4"}) {
		t.Fatalf("unexpected persisted order: %v", got)
	}
}

func TestReadAuditRotation(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	path := filepath.Join(s.Dir, "audit.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxAuditBytes / 2); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := s.AuditReadOnly(auditEvent(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "audit.previous")); !os.IsNotExist(err) {
		t.Fatal("buffering rotated audit")
	}
	if err := s.Audit(auditEvent(2)); err != nil {
		t.Fatal(err)
	}
	prior, err := os.Stat(filepath.Join(s.Dir, "audit.previous"))
	if err != nil || prior.Size() != maxAuditBytes/2 || len(auditEvents(t, s)) != 2 {
		t.Fatal("rotation contract changed")
	}
}

func TestReadAuditConcurrentAppendAndFlush(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				if err := s.AuditReadOnly(auditEvent(i*25 + j)); err != nil {
					t.Error(err)
				}
				if err := s.FlushAudit(); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
	if err := s.FlushAudit(); err != nil {
		t.Fatal(err)
	}
	if len(auditEvents(t, s)) != 100 {
		t.Fatal("concurrent flush lost/duplicated events")
	}
}

func TestReadAuditPollAndStop(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if auditCheckInterval < time.Minute {
		t.Fatal("short audit polling interval")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.auditLoop(ctx, ticks, func() bool { return false })
	}()
	if err := s.AuditReadOnly(auditEvent(1)); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	cancel()
	<-done
	// The service drains callers before this final flush.
	if err := s.FlushAudit(); err != nil || len(auditEvents(t, s)) != 1 {
		t.Fatal("stop did not flush")
	}
}

func TestWebAPIReadOnlyAuditClassification(t *testing.T) {
	p := WebAPIProfile{APIs: map[string]map[string][]int{
		"SYNO.Cal.Event": {"list": {6}, "get": {6}, "create": {6}, "delete": {6}, "get_admin": {6}},
		"SYNO.Cal.Batch": {"get": {1}},
		"SYNO.Unknown":   {"get": {1}},
	}}
	for _, c := range []struct {
		api, method string
		version     int
		want        bool
	}{
		{"SYNO.Cal.Event", "list", 6, true},
		{"SYNO.Cal.Event", "get", 6, true},
		{"SYNO.Cal.Event", "list", 5, false},
		{"SYNO.Cal.Event", "create", 6, false},
		{"SYNO.Cal.Event", "delete", 6, false},
		{"SYNO.Cal.Event", "get_admin", 6, false},
		{"SYNO.Cal.Batch", "get", 1, false},
		{"SYNO.Unknown", "get", 1, false},
		{"SYNO.Cal.Cal", "list", 5, false},
	} {
		if got := p.readOnly(c.api, c.method, c.version); got != c.want {
			t.Errorf("%s/%s/%d: %v, want %v", c.api, c.method, c.version, got, c.want)
		}
	}
}

func TestWebAPIReadAuditNoPerCallPersistentWrites(t *testing.T) {
	e, _ := webSetup(t)
	id := webGrant(t, e, true)
	snapshot := func() map[string]string {
		out := map[string]string{}
		entries, err := os.ReadDir(e.Store.Dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, ent := range entries {
			b, err := os.ReadFile(filepath.Join(e.Store.Dir, ent.Name()))
			if err != nil {
				t.Fatal(err)
			}
			out[ent.Name()] = string(b)
		}
		return out
	}
	before := snapshot()
	for i := 0; i < 200; i++ {
		if r := webCall(e, id, true, nil); !r.OK {
			t.Fatal(r)
		}
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("periodic reads changed audit/grants/operation records")
	}
	if r := webCall(e, id, true, map[string]any{"method": "create"}); !r.OK {
		t.Fatal(r)
	}
	events := auditEvents(t, e.Store)
	if events[len(events)-2].Result != "admitting" || events[len(events)-1].Result != "completed" ||
		!strings.Contains(events[len(events)-1].Path, "/create/") {
		t.Fatal("mutation was not synchronously audited")
	}
}

func TestReadAuditStorageFailureDoesNotBlockReadsOrAdmitWrites(t *testing.T) {
	e, f := webSetup(t)
	id := webGrant(t, e, true)
	path := filepath.Join(e.Store.Dir, "audit.jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if r := webCall(e, id, true, nil); !r.OK || f.calls != 1 {
		t.Fatal("read depended on persistent audit availability", r)
	}
	if r := webCall(e, id, true, map[string]any{"method": "create"}); r.Code != "STORAGE_ERROR" || f.calls != 1 {
		t.Fatal("write dispatched without durable audit", r)
	}
}

func TestBufferedAuditReadbackAndDropMarkerOwnerFiltering(t *testing.T) {
	e, f := webSetup(t)
	id := webGrant(t, e, true)
	for i := 0; i < 200; i++ {
		if r := webCall(e, id, true, nil); !r.OK {
			t.Fatal(r)
		}
	}
	// Force byte-bound overflow with fewer than the reader's 256 rows, so the
	// marker remains in its ordinary tail window.
	for i := 0; i < 30; i++ {
		event := AuditEvent{At: e.Now(), Package: e.Policy.Package, Username: "alice", UID: f.user.UID, Operation: "webapi.call", Path: strings.Repeat("x", 8192), Result: "completed"}
		if err := e.Store.AuditReadOnly(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Store.FlushAudit(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		admin bool
		want  bool
	}{
		{"alice", false, false},
		{"bob", false, false},
		{"bob", true, true},
	} {
		f.user.Username, f.user.Admin = c.name, c.admin
		r := request(e, "delegate.audit", map[string]any{})
		b, _ := json.Marshal(r.Result)
		if !r.OK || strings.Contains(string(b), "dropped:") != c.want {
			t.Fatalf("drop marker ownership %s/%v: %+v", c.name, c.admin, r)
		}
		if c.name == "bob" && !c.admin && strings.Contains(string(b), "alice") {
			t.Fatal("buffered audit leaked another user's events")
		}
	}
}
