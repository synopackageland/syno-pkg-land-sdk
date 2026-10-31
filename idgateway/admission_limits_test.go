package idgateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPreAuthDoesNotAcquireSessionLock(t *testing.T) {
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	f.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := httptest.NewRequest("GET", "https://nas.example:18443/", nil)
		f.bounce(httptest.NewRecorder(), r, txn{}, "https://nas.example:18443")
	}()
	select {
	case <-done:
		f.mu.Unlock()
	case <-time.After(time.Second):
		f.mu.Unlock()
		<-done
		t.Fatal("pre-auth admission took the session mutex")
	}
}

func TestTransactionFloodCannotEvictOtherClientsOrSessions(t *testing.T) {
	f, c := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	f.sessions["live"] = session{subject: alice, ip: "192.0.2.8", expires: c.now().Add(time.Hour)}
	bounce := func(ip string) int {
		r := httptest.NewRequest("GET", "https://nas.example:18443/", nil)
		r.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		f.bounce(w, r, txn{}, "https://nas.example:18443")
		return w.Code
	}
	for i := 0; i < 64; i++ {
		if got := bounce("192.0.2.7"); got != http.StatusFound {
			t.Fatal(i, got)
		}
	}
	if got := bounce("192.0.2.7"); got != http.StatusTooManyRequests {
		t.Fatal("client cap absent", got)
	}
	if got := bounce("192.0.2.8"); got != http.StatusFound {
		t.Fatal("another client denied", got)
	}
	if _, ok := f.sessions["live"]; !ok {
		t.Fatal("preauth evicted a live session")
	}
	c.t = c.t.Add(TxnTTL + time.Second)
	if got := bounce("192.0.2.7"); got != http.StatusFound {
		t.Fatal("expired capacity not recovered", got)
	}
}

func TestSessionBootstrapCapPreservesOtherUsers(t *testing.T) {
	f, c := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	for i := 0; i < 64; i++ {
		if !f.addSession(string(rune(i)), session{subject: alice, ip: "192.0.2.7", expires: c.now().Add(time.Hour)}) {
			t.Fatal("early cap")
		}
	}
	if f.addSession("extra", session{subject: alice, ip: "192.0.2.7", expires: c.now().Add(time.Hour)}) {
		t.Fatal("session cap absent")
	}
	if !f.addSession("other", session{subject: Subject{UID: 1027, Name: "bob"}, ip: "192.0.2.8", expires: c.now().Add(time.Hour)}) {
		t.Fatal("unrelated client denied")
	}
	f.RevokeUID(alice.UID)
	if !f.addSession("new", session{subject: alice, ip: "192.0.2.7", expires: c.now().Add(time.Hour)}) {
		t.Fatal("revocation did not release capacity")
	}
}
