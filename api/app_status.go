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

// AppLanguage reads an App's trusted DSM language preference, if available.
// An empty result uses the browser's first Accept-Language tag instead.
type AppLanguage func(context.Context, Caller) (string, error)

// AppStatusHandler can be registered with HandleRequest("app.status", ...).
// Keep app.status authenticated in the App's permissions.json. language may
// be nil: browser language is the fallback, never a claim about a DSM setting.
func AppStatusHandler(packageID, version string, language AppLanguage) RequestHandler {
	return func(ctx context.Context, caller Caller, r *http.Request, _ json.RawMessage) (any, error) {
		if !caller.Authenticated() {
			return nil, &Error{Code: "session_required", Message: "sign in to DSM again"}
		}
		lang := ""
		if language != nil {
			value, err := language(ctx, caller)
			if err != nil {
				return nil, err
			}
			lang = value
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

func appLanguage(lang string) (string, string) {
	aliases := map[string]string{"enu": "en", "cht": "zh-TW", "chs": "zh-CN", "jpn": "ja", "krn": "ko", "ger": "de", "fre": "fr", "spn": "es", "ita": "it"}
	if locale, ok := aliases[lang]; ok {
		return lang, locale
	}
	if len(lang) <= 35 && appLanguageTag.MatchString(lang) {
		return "", lang
	}
	return "", ""
}
