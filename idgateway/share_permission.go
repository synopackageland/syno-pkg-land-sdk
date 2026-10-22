package idgateway

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
)

func writeShareSignIn(w http.ResponseWriter, r *http.Request) {
	locale, m := denialMessages(r)
	w.Header().Set("Content-Language", locale)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Accept-Language")
	http.Error(w, strings.TrimRight(m["sign_in_required"], ".。"), http.StatusUnauthorized)
}

// The stopped package has no Vue server. Its existing DSM CGI entry remains
// available and renders a native nonmodal dialog. Retry is a read-only GET;
// starting the package remains a deliberate DSM Package Center action.
func writeSharePermission(w http.ResponseWriter, r *http.Request, err error) {
	locale, m := denialMessages(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", locale)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Accept-Language")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'self'")
	message := m["share_ready"]
	var permission *sharedfolder.PermissionError
	code := "ready"
	if errors.As(err, &permission) {
		access := m["share_read"]
		if permission.Need == sharedfolder.Write {
			access = m["share_write"]
		}
		message = strings.NewReplacer("{folder}", permission.Name, "{account}", permission.Account, "{access}", access).Replace(m["share_steps"])
		code = "shared_folder_permission"
	} else if err != nil {
		message = m["share_unavailable"]
		code = "shared_folder_unavailable"
	}
	fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>:root{color-scheme:light dark}body{font:16px system-ui;margin:0;padding:24px;line-height:1.6}main{max-width:640px;margin:0 auto}dialog{position:static;box-sizing:border-box;width:100%%;margin:24px 0;padding:24px;border:1px solid GrayText;border-radius:12px;overflow-wrap:anywhere}h1{font-size:1.25rem;margin-top:0}footer{display:flex;flex-wrap:wrap;gap:12px}button{font:inherit;padding:8px 16px;cursor:pointer}a{color:LinkText}</style><main><p>%s</p><dialog open aria-labelledby="permission-title"><h1 id="permission-title">%s</h1><p data-error-code="%s">%s</p><footer><form method="get"><input type="hidden" name="sharedfolder" value="retry"><button type="submit">%s</button></form><form method="dialog"><button>%s</button></form></footer></dialog></main></html>`, locale, html.EscapeString(m["share_title"]), html.EscapeString(m["share_next_start"]), html.EscapeString(m["share_title"]), code, html.EscapeString(message), html.EscapeString(m["share_retry"]), html.EscapeString(m["share_dismiss"]))
}
