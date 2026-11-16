package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
)

// DSM's own web server on this NAS. Only loopback: DSM binds a browser
// session to its address and revokes it when another address replays it,
// while it accepts the same session from 127.0.0.1.
var dsmLanguageEndpoint = "http://127.0.0.1:5000/webapi/entry.cgi"

const (
	// appLanguageRefresh is how old a known language may be before a
	// long-running backend reads it again. It is not a deadline: the known
	// value is still answered while the background read runs.
	appLanguageRefresh = 10 * time.Minute
	// appLanguageUsers bounds the per-process cache; the oldest entry goes first.
	appLanguageUsers = 256
	// appLanguageReply bounds DSM's reply; UserSettings also carries desktop items.
	appLanguageReply = 1 << 20
)

var dsmLanguageCode = regexp.MustCompile(`^[a-z]{3}$`)

type knownLanguage struct {
	lang    string
	at      time.Time
	reading bool
}

// languageCache holds each user's DSM display language: a DSM code from
// dsmLocales, or "" to follow the browser.
type languageCache struct {
	mu          sync.Mutex
	users       map[string]*knownLanguage
	now         func() time.Time
	lookup      func(context.Context, map[string]string) (string, bool)
	longRunning func() bool
}

var defaultLanguages = newLanguageCache()

func newLanguageCache() *languageCache {
	return &languageCache{
		users:  map[string]*knownLanguage{},
		now:    time.Now,
		lookup: readDSMLanguage,
		// A CGI process ends with its reply, so a background read could not
		// finish there; the entry page's hint is its only DSM source.
		longRunning: func() bool { return os.Getenv("GATEWAY_INTERFACE") == "" },
	}
}

// preference never waits for DSM. A hint is stored and answered; otherwise
// the known value is answered and a missing or old one is read in the
// background, at most one read per user at a time.
func (c *languageCache) preference(user string, r *http.Request, raw json.RawMessage) string {
	if user == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if hint, ok := languageHint(raw); ok {
		c.store(user, hint, c.now())
		return hint
	}
	known := c.users[user]
	if known != nil && c.now().Sub(known.at) < appLanguageRefresh {
		return known.lang
	}
	if known != nil && known.reading || !c.longRunning() {
		return knownLang(known)
	}
	auth, err := DSMSession(r)
	if err != nil {
		return knownLang(known)
	}
	if known == nil {
		known = c.store(user, "", time.Time{})
	}
	known.reading = true
	go c.read(user, auth, c.now())
	return known.lang
}

func knownLang(known *knownLanguage) string {
	if known == nil {
		return ""
	}
	return known.lang
}

func (c *languageCache) read(user string, auth map[string]string, started time.Time) {
	// The reply has already gone; the bound only frees this user's next
	// read if DSM never answers. It is the SDK's operation budget.
	ctx, cancel := context.WithTimeout(context.Background(), runtimebudget.Operation)
	defer cancel()
	lang, ok := c.lookup(ctx, auth)
	c.mu.Lock()
	defer c.mu.Unlock()
	known := c.users[user]
	if known == nil {
		if !ok {
			return
		}
		known = c.store(user, lang, c.now())
	}
	known.reading = false
	// A hint that arrived during the read is newer than this answer.
	if known.at.After(started) {
		return
	}
	// A failure keeps the previous value and waits a refresh period to retry.
	known.at = c.now()
	if ok {
		known.lang = lang
	}
}

func (c *languageCache) store(user, lang string, at time.Time) *knownLanguage {
	known := c.users[user]
	if known == nil {
		if len(c.users) >= appLanguageUsers {
			oldest := ""
			for name, entry := range c.users {
				if oldest == "" || entry.at.Before(c.users[oldest].at) {
					oldest = name
				}
			}
			delete(c.users, oldest)
		}
		known = &knownLanguage{}
		c.users[user] = known
	}
	known.lang, known.at = lang, at
	return known
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
	return dsmLanguage(value), true
}

// dsmLanguage is a DSM code the SDK maps, or "" (def, or no SDK locale).
func dsmLanguage(code string) string {
	if _, ok := dsmLocales[code]; ok {
		return code
	}
	return ""
}

// dsmPreference: the personal language, else the system language when the
// personal one is "def". ok is false only when the personal one is unknown.
func dsmPreference(personal string, personalOK bool, system string, systemOK bool) (string, bool) {
	if !personalOK {
		return "", false
	}
	if personal != "" && personal != "def" {
		return dsmLanguage(personal), true
	}
	if systemOK && system != "def" {
		return dsmLanguage(system), true
	}
	return "", true
}

// readDSMLanguage asks DSM, as the caller, for the personal and system
// display languages in one compound request. Users outside administrators
// are refused the system language (105), so their "def" follows the browser.
func readDSMLanguage(ctx context.Context, auth map[string]string) (string, bool) {
	compound := `[{"api":"SYNO.Core.UserSettings","method":"get","version":1},{"api":"SYNO.Core.Region.Language","method":"get","version":1}]`
	form := url.Values{"api": {"SYNO.Entry.Request"}, "method": {"request"}, "version": {"1"}, "stop_when_error": {"false"}, "compound": {compound}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dsmLanguageEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-SYNO-TOKEN", auth["dsm_synotoken"])
	req.AddCookie(&http.Cookie{Name: "id", Value: auth["dsm_cookie"]})
	client := http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, appLanguageReply+1))
	var reply struct {
		Success bool `json:"success"`
		Data    struct {
			Result []struct {
				API     string          `json:"api"`
				Success bool            `json:"success"`
				Data    json.RawMessage `json:"data"`
			} `json:"result"`
		} `json:"data"`
	}
	if err != nil || len(body) > appLanguageReply || res.StatusCode != http.StatusOK || json.Unmarshal(body, &reply) != nil || !reply.Success {
		return "", false
	}
	var personal, system string
	var personalOK, systemOK bool
	for _, result := range reply.Data.Result {
		switch {
		case result.API == "SYNO.Core.UserSettings" && result.Success:
			var data struct {
				Personal struct {
					Lang string `json:"lang"`
				} `json:"Personal"`
			}
			personalOK = json.Unmarshal(result.Data, &data) == nil
			personal = data.Personal.Lang
		case result.API == "SYNO.Core.Region.Language" && result.Success:
			var data struct {
				Language string `json:"language"`
			}
			systemOK = json.Unmarshal(result.Data, &data) == nil && data.Language != ""
			system = data.Language
		}
	}
	return dsmPreference(personal, personalOK, system, systemOK)
}
