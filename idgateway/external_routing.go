package idgateway

import (
	"context"
	"fmt"
	"html"
	"net/http"
)

type externalSnapshotKey struct{}

func requestExternal(r *http.Request, read func() External) External {
	if e, ok := r.Context().Value(externalSnapshotKey{}).(External); ok {
		return e
	}
	if read == nil {
		return External{}
	}
	return read()
}
func withExternal(r *http.Request, e External) *http.Request {
	if len(e.Accept) == 0 {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), externalSnapshotKey{}, e))
}
func (f *Front) external(r *http.Request) External { return requestExternal(r, f.cfg.External) }

// The public explanation must reveal no hostnames or settings values.
func writeNoDSMOrigin(w http.ResponseWriter, r *http.Request) {
	locale, messages := denialMessages(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", locale)
	w.Header().Add("Vary", "Accept-Language")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset="utf-8"><title>%s</title><p data-error-code="dsm_origin_missing" data-i18n="gateway.external_dsm_missing">%s</p></html>`, locale, html.EscapeString(messages["external_sign_in_unavailable"]), html.EscapeString(messages["external_dsm_missing"]))
}
