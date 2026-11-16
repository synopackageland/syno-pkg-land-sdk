package api

import (
	"encoding/json"
	"regexp"
	"sync"
)

// appLanguageUsers bounds the per-process cache; the least recently stored
// user goes first.
const appLanguageUsers = 256

var dsmLanguageCode = regexp.MustCompile(`^[a-z]{3}$`)

// languageCache holds each user's DSM display language as the entry page last
// reported it: a DSM code from dsmLocales, or "" to follow the browser. The
// backend never asks DSM itself; replaying the browser's session from the
// backend can revoke it.
type languageCache struct {
	mu    sync.Mutex
	users map[string]string
	order []string
}

var defaultLanguages = newLanguageCache()

func newLanguageCache() *languageCache {
	return &languageCache{users: map[string]string{}}
}

// preference stores and answers the request's hint, else the user's last
// known hint, else "" (the browser's language).
func (c *languageCache) preference(user string, raw json.RawMessage) string {
	if user == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if hint, ok := languageHint(raw); ok {
		c.store(user, hint)
		return hint
	}
	return c.users[user]
}

func (c *languageCache) store(user, lang string) {
	if _, ok := c.users[user]; ok {
		for i, name := range c.order {
			if name == user {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	} else if len(c.users) >= appLanguageUsers {
		delete(c.users, c.order[0])
		c.order = c.order[1:]
	}
	c.users[user] = lang
	c.order = append(c.order, user)
}

// languageHint reads app.status {"dsmLanguage": "cht"}: the DSM display
// language the entry page read from DSM on its own origin. "" or "def"
// means DSM follows the browser. It is display information only.
func languageHint(raw json.RawMessage) (string, bool) {
	var params struct {
		DSMLanguage *string `json:"dsmLanguage"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &params) != nil || params.DSMLanguage == nil {
		return "", false
	}
	value := *params.DSMLanguage
	if value != "" && !dsmLanguageCode.MatchString(value) {
		return "", false
	}
	if _, ok := dsmLocales[value]; !ok {
		return "", true
	}
	return value, true
}
