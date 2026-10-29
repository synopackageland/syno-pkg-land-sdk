package dsm

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// MaxSessionCacheTTL bounds how long a daemon may trust a verified session.
const MaxSessionCacheTTL = time.Hour

const sessionCacheEntries = 4096

// SessionCache is an api.Authenticator for a resident daemon that remembers
// each verified DSM session (the id cookie, SynoToken and client address
// together) for ttl, so a busy page does not run authenticate.cgi and the
// administrator check on every request. Only successes are kept.
//
// The cost is staleness: for up to ttl after a sign-out, a disabled account
// or a lost administrator role, a request carrying that session's cookie and
// SynoToken is still accepted with the identity it had. Wrap only the session
// authenticator, inside dsm.NewEntryGate, so App entry decisions keep their
// own EntryCacheTTL. A CGI gains nothing from it: each request is a new process.
type SessionCache struct {
	next    api.Authenticator
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[[32]byte]cachedSession
}

type cachedSession struct {
	id api.Identity
	at time.Time
}

// CacheSessions wraps next with a SessionCache of ttl (1s to MaxSessionCacheTTL).
func CacheSessions(next api.Authenticator, ttl time.Duration) (*SessionCache, error) {
	if next == nil || ttl < time.Second || ttl > MaxSessionCacheTTL {
		return nil, errInvalidSessionCache
	}
	return &SessionCache{next: next, ttl: ttl, now: time.Now, entries: map[[32]byte]cachedSession{}}, nil
}

var errInvalidSessionCache = errors.New("session cache needs an authenticator and a TTL from 1s to 1h")

func (c *SessionCache) Authenticate(r *http.Request) (api.Identity, error) {
	key, ok := sessionKey(r)
	if !ok {
		return c.next.Authenticate(r)
	}
	now := c.now()
	c.mu.Lock()
	hit, found := c.entries[key]
	c.mu.Unlock()
	if found && !now.Before(hit.at) && now.Sub(hit.at) < c.ttl {
		return hit.id, nil
	}
	id, err := c.next.Authenticate(r)
	if err != nil || id.User == "" {
		return id, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= sessionCacheEntries {
		for k, e := range c.entries {
			if now.Sub(e.at) >= c.ttl || now.Before(e.at) {
				delete(c.entries, k)
			}
		}
		// Still full of live sessions: start over rather than grow.
		if len(c.entries) >= sessionCacheEntries {
			c.entries = map[[32]byte]cachedSession{}
		}
	}
	c.entries[key] = cachedSession{id: id, at: now}
	return id, nil
}

// sessionKey is everything authenticate.cgi sees (sessionEnv): the whole
// Cookie header, the SynoToken and the client address. Without a DSM session
// cookie there is nothing to remember.
func sessionKey(r *http.Request) ([32]byte, bool) {
	if cookie, err := r.Cookie("id"); err != nil || cookie.Value == "" {
		return [32]byte{}, false
	}
	addr, err := api.ClientAddress(r)
	if err != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(r.Header.Get("Cookie") + "\x00" + r.Header.Get("X-SYNO-TOKEN") + "\x00" + addr)), true
}
