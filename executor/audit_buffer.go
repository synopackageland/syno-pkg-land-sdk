package main

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"
)

const (
	maxBufferedAuditEvents = 256
	maxBufferedAuditBytes  = 64 << 10
	auditCheckInterval     = time.Minute
)

// Read audit is observational, not an authority or a durable operation record.
// Keep serialized events so both limits account for the exact JSONL bytes.
type auditBuffer struct {
	mu             sync.Mutex
	events         [][]byte
	bytes          int
	dropped        uint64
	marker         AuditEvent
	directoryDirty bool
}

// AuditReadOnly never inspects or opens persistent storage, even on overflow.
// Unknown WebAPI methods and every authority-changing event use Audit instead.
func (s *Store) AuditReadOnly(event AuditEvent) error {
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	s.audit.mu.Lock()
	defer s.audit.mu.Unlock()
	q := &s.audit
	q.marker = AuditEvent{At: event.At, Package: event.Package, Operation: "audit.dropped"}
	drop := func() {
		if q.dropped != ^uint64(0) {
			q.dropped++
		}
	}
	if len(b) > maxBufferedAuditBytes {
		drop()
		return nil
	}
	for len(q.events) >= maxBufferedAuditEvents || q.bytes+len(b) > maxBufferedAuditBytes {
		q.bytes -= len(q.events[0])
		copy(q.events, q.events[1:])
		q.events[len(q.events)-1] = nil
		q.events = q.events[:len(q.events)-1]
		drop()
	}
	q.events = append(q.events, b)
	q.bytes += len(b)
	return nil
}

// FlushAudit is called after draining active callers on graceful stop, and
// before mutations by Audit. Crashes can lose the last buffered read events.
func (s *Store) FlushAudit() error {
	s.audit.mu.Lock()
	defer s.audit.mu.Unlock()
	return s.flushAuditLocked(nil)
}

func (s *Store) flushAuditLocked(tail []byte) error {
	q := &s.audit
	if len(q.events) == 0 && q.dropped == 0 && len(tail) == 0 {
		return nil
	}
	b := make([]byte, 0, q.bytes+len(tail)+256)
	if q.dropped > 0 {
		marker := q.marker
		marker.Result = "dropped:" + strconv.FormatUint(q.dropped, 10)
		line, err := json.Marshal(marker)
		if err != nil {
			return err
		}
		b = append(append(b, line...), '\n')
	}
	for _, event := range q.events {
		b = append(b, event...)
	}
	b = append(b, tail...)
	if err := s.appendAudit(b); err != nil {
		return err
	}
	clear(q.events)
	q.events = q.events[:0]
	q.bytes, q.dropped = 0, 0
	q.marker = AuditEvent{}
	return nil
}

func (s *Store) FlushAuditIfAwake(awake func() bool) error {
	// Do not even probe sysfs when there is nothing to flush.
	s.audit.mu.Lock()
	defer s.audit.mu.Unlock()
	if len(s.audit.events) == 0 && s.audit.dropped == 0 {
		return nil
	}
	if !awake() {
		return nil
	}
	return s.flushAuditLocked(nil)
}

func (s *Store) auditLoop(ctx context.Context, ticks <-chan time.Time, awake func() bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			// Retain the bounded ring on failure. Logging each failed periodic
			// attempt could itself write the persistent executor log.
			_ = s.FlushAuditIfAwake(awake)
		}
	}
}

func (e *Executor) auditWebAPILocked(s Subject, d Delegation, p webAPIRequest, target, result string) error {
	event := AuditEvent{At: e.Now().UTC(), Package: e.Policy.Package, Username: s.Username, UID: s.UID, Operation: "webapi.call", Job: d.ID, Path: target, Result: result}
	if e.Policy.WebAPI[d.Scope].readOnly(p.API, p.Method, p.Version) {
		return e.Store.AuditReadOnly(event)
	}
	return e.Store.Audit(event)
}
