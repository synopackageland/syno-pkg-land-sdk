package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// AppStatus is entry-page display metadata. The server still checks the real
// session and Application Privileges on every request.
type AppStatus struct {
	Package string `json:"package"`
	Version string `json:"version"`
	User    string `json:"user"`
	Admin   bool   `json:"admin"`
	Lang    string `json:"lang"`
	Locale  string `json:"locale"`
}

// AppLanguage replaces the SDK's DSM display-language lookup with the App's
// own preference: a DSM language code (cht, enu, …) or a BCP 47 tag. An empty
// result or an error uses the browser's first Accept-Language tag instead.
type AppLanguage func(context.Context, Caller, *http.Request) (string, error)

// AppStatusHandler can be registered with HandleRequest("app.status", ...).
// Keep app.status authenticated in the App's permissions.json.
//
// With a nil language the caller's DSM display language comes first: the
// entry page's dsmLanguage hint, else the last hint known for this user in
// this process. "def" (DSM follows the browser), no hint and a language
// without an SDK locale use the browser's Accept-Language. The backend never
// reads DSM with the browser's session. See docs/entry-page.md.
func AppStatusHandler(packageID, version string, language AppLanguage) RequestHandler {
	return func(ctx context.Context, caller Caller, r *http.Request, raw json.RawMessage) (any, error) {
		if !caller.Authenticated() {
			return nil, &Error{Code: "session_required", Message: "sign in to DSM again"}
		}
		lang := ""
		if language != nil {
			if value, err := language(ctx, caller, r); err == nil {
				lang = value
			}
		} else {
			lang = defaultLanguages.preference(caller.User(), raw)
		}
		if lang == "" && r != nil {
			lang, _, _ = strings.Cut(r.Header.Get("Accept-Language"), ",")
			lang, _, _ = strings.Cut(lang, ";")
			lang = strings.TrimSpace(lang)
		}
		lang, locale := appLanguage(lang)
		return AppStatus{Package: packageID, Version: version, User: caller.User(), Admin: caller.Admin(), Lang: lang, Locale: locale}, nil
	}
}

var appLanguageTag = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,3}$`)

// dsmLocales maps DSM display-language codes to the SDK's UI locales.
var dsmLocales = map[string]string{"enu": "en", "cht": "zh-TW", "chs": "zh-CN", "jpn": "ja", "krn": "ko", "ger": "de", "fre": "fr", "spn": "es", "ita": "it"}

func appLanguage(lang string) (string, string) {
	if locale, ok := dsmLocales[lang]; ok {
		return lang, locale
	}
	if len(lang) <= 35 && appLanguageTag.MatchString(lang) {
		return "", lang
	}
	return "", ""
}
