package idgateway

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"golang.org/x/text/language"
)

// The gateway runs without the App's frontend. Keep its catalogs self-contained
// and use the same locale names as the SDK's TypeScript catalogs.
//
//go:embed i18n/*.json
var denialCatalogs embed.FS

var denialLocales = []string{"en", "de", "es", "fr", "it", "ja", "ko", "zh-CN", "zh-TW"}
var denialMatcher = language.NewMatcher(func() []language.Tag {
	tags := make([]language.Tag, len(denialLocales))
	for i, locale := range denialLocales {
		tags[i] = language.MustParse(locale)
	}
	return tags
}())

func denialMessages(r *http.Request) (string, map[string]string) {
	tags, _, err := language.ParseAcceptLanguage(r.Header.Get("Accept-Language"))
	index := 0
	if err == nil {
		_, matched, confidence := denialMatcher.Match(tags...)
		if confidence != language.No {
			index = matched
		}
	}
	locale := denialLocales[index]
	raw, _ := denialCatalogs.ReadFile("i18n/" + locale + ".json")
	var messages map[string]string
	// Embedded catalogs are checked for completeness and validity by the tests.
	if err := json.Unmarshal(raw, &messages); err != nil {
		panic(err)
	}
	return locale, messages
}

func admissionCode(err error) string {
	if errors.Is(err, api.ErrAppPrivilegeDenied) {
		return "app_privilege_denied"
	}
	if errors.Is(err, api.ErrAdminRequired) || errors.Is(err, ErrGroupRequired) {
		return "access_denied"
	}
	return "temporarily_unavailable"
}

// writeFailure uses stable gateway codes and message keys in HTML and JSON.
// Only missing sessions and verification failures offer a user-initiated retry;
// retrying cannot repair an authenticated account's missing permissions.
func (f *Front) writeFailure(w http.ResponseWriter, r *http.Request, code, retry string, page bool) {
	status, stable, key := http.StatusServiceUnavailable, "dsm_verification_failed", "verification_failed"
	switch code {
	case "app_privilege_denied":
		status, stable, key = http.StatusForbidden, "dsm_app_privilege_denied", "app_privilege_denied"
	case "access_denied":
		status, stable, key = http.StatusForbidden, "dsm_admin_required", "admin_required"
		if f.cfg.Access == "dsm-groups" {
			stable, key = "dsm_group_required", "group_required"
		}
	case "login_required":
		status, stable, key = http.StatusUnauthorized, "dsm_sign_in_required", "sign_in_required"
	}
	locale, messages := denialMessages(r)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Language", locale)
	w.Header().Add("Vary", "Accept-Language")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !page {
		writeJSON(w, status, map[string]string{"error": stable, "message_key": "gateway." + key, "message": messages[key]})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><p data-error-code="%s" data-i18n="gateway.%s">%s</p>`, locale, html.EscapeString(messages["title"]), stable, key, html.EscapeString(messages[key]))
	if status != http.StatusForbidden {
		// A public origin signs in at the configured DSM origin, as bounce
		// does; the DSM port on the public hostname is not reachable there.
		signIn := f.dsmOrigin(r)
		if external := f.external(r); external.Index(requestOrigin(r)) >= 0 {
			signIn = external.DSM
		}
		fmt.Fprint(w, `<p>`)
		if signIn != "" {
			fmt.Fprintf(w, `<a data-i18n="gateway.sign_in" href="%s/">%s</a> · `, html.EscapeString(signIn), html.EscapeString(messages["sign_in"]))
		}
		fmt.Fprintf(w, `<a data-i18n="gateway.retry" href="%s">%s</a></p>`, html.EscapeString(localTarget(retry)), html.EscapeString(messages["retry"]))
	}
	fmt.Fprint(w, "</html>")
}

// writeStarting answers while the App is not ready yet, for example during a
// slow start or a temporary stall on a busy NAS. The lifecycle supervisor, not
// the gateway, decides when that becomes a failure.
func writeStarting(w http.ResponseWriter, r *http.Request) {
	locale, messages := denialMessages(r)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Language", locale)
	w.Header().Add("Vary", "Accept-Language")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Retry-After", "10")
	if !isNavigation(r) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "app_starting", "message_key": "gateway.starting", "message": messages["starting"]})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="refresh" content="10"><title>%s</title><p data-error-code="app_starting" data-i18n="gateway.starting">%s</p></html>`, locale, html.EscapeString(messages["starting_title"]), html.EscapeString(messages["starting"]))
}
