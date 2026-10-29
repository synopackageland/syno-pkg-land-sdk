package dsm

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type countingAuth struct {
	calls int
	err   error
}

func (a *countingAuth) Authenticate(*http.Request) (api.Identity, error) {
	a.calls++
	if a.err != nil {
		return api.Identity{}, a.err
	}
	return api.Identity{User: "alice", Admin: a.calls == 1}, nil
}

func cacheRequest(cookie, token, addr string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	r.Header.Set("X-SYNO-TOKEN", token)
	r.RemoteAddr = addr + ":0"
	return r
}

func TestSessionCacheKeepsVerifiedSessionsForTTL(t *testing.T) {
	next := &countingAuth{}
	c, err := CacheSessions(next, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	auth := func(cookie, token, addr string) api.Identity {
		t.Helper()
		id, err := c.Authenticate(cacheRequest(cookie, token, addr))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	if id := auth("id=s1", "t1", "192.0.2.1"); !id.Admin {
		t.Fatalf("first identity %+v", id)
	}
	// The cached identity, administrator flag included, until the TTL ends.
	now = now.Add(9 * time.Minute)
	if id := auth("id=s1", "t1", "192.0.2.1"); !id.Admin || next.calls != 1 {
		t.Fatalf("not cached: %+v after %d calls", id, next.calls)
	}
	for _, other := range [][3]string{{"id=s2", "t1", "192.0.2.1"}, {"id=s1", "t2", "192.0.2.1"}, {"id=s1", "t1", "192.0.2.2"}, {"id=s1; _SSID=x", "t1", "192.0.2.1"}} {
		before := next.calls
		auth(other[0], other[1], other[2])
		if next.calls != before+1 {
			t.Fatalf("%v reused another session", other)
		}
	}
	now = now.Add(time.Minute)
	before := next.calls
	if id := auth("id=s1", "t1", "192.0.2.1"); id.Admin || next.calls != before+1 {
		t.Fatalf("expired entry reused: %+v", id)
	}
	// No DSM session cookie: never cached.
	before = next.calls
	auth("other=1", "t1", "192.0.2.1")
	auth("other=1", "t1", "192.0.2.1")
	if next.calls != before+2 {
		t.Fatal("cached a request without a session cookie")
	}
}

func TestSessionCacheNeverKeepsFailures(t *testing.T) {
	next := &countingAuth{err: api.ErrNoSession}
	c, _ := CacheSessions(next, time.Minute)
	for i := 0; i < 2; i++ {
		if _, err := c.Authenticate(cacheRequest("id=s1", "t", "192.0.2.1")); !errors.Is(err, api.ErrNoSession) {
			t.Fatal(err)
		}
	}
	if next.calls != 2 {
		t.Fatalf("failure cached: %d calls", next.calls)
	}
	for _, ttl := range []time.Duration{0, time.Millisecond, 2 * time.Hour} {
		if _, err := CacheSessions(next, ttl); err == nil {
			t.Fatalf("accepted TTL %v", ttl)
		}
	}
}

func TestSessionCacheIsBounded(t *testing.T) {
	c, _ := CacheSessions(&countingAuth{}, time.Hour)
	for i := 0; i < sessionCacheEntries+10; i++ {
		c.Authenticate(cacheRequest("id=s"+string(rune('a'+i%26))+time.Duration(i).String(), "t", "192.0.2.1"))
	}
	if len(c.entries) > sessionCacheEntries {
		t.Fatalf("%d entries", len(c.entries))
	}
}
