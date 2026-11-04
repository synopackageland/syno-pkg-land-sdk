package idgateway

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// SecureContextVersion lets packaging tools verify the selected SDK source.
const SecureContextVersion = 1

func validateSecureContext(policy string) error {
	if policy != "" && policy != "required" {
		return errors.New("secure_context must be required or omitted")
	}
	return nil
}

// Authentication retains ReadDSMPorts' legacy defaults. Guidance must use an
// actual, unambiguous declaration, never those defaults or forwarded headers.
func declaredDSMHTTPSPort(dsmPath, synoinfoPath string) int {
	if port, declared := declaredDSMJSONHTTPSPort(dsmPath); declared {
		return port
	}
	return declaredSynoinfoHTTPSPort(synoinfoPath)
}

func declaredDSMJSONHTTPSPort(path string) (port int, declared bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(raw, &config) != nil || config == nil {
		return 0, false
	}
	ssl := strings.TrimSpace(string(config["ssl"]))
	// DSM's nginx template renders its HTTPS listener inside the ssl section.
	// An absent/null/false section must not revive a stale synoinfo port.
	// ssl.redirect controls only HTTP redirection, not HTTPS availability.
	if ssl == "" || ssl == "null" || ssl == "false" {
		return 0, true
	}
	var listener struct {
		Port *int `json:"port"`
	}
	if json.Unmarshal([]byte(ssl), &listener) != nil || listener.Port == nil {
		return 0, false
	}
	if *listener.Port == 0 {
		return 0, true
	}
	if !validPort(*listener.Port, false) {
		return 0, false
	}
	return *listener.Port, true
}

func declaredSynoinfoHTTPSPort(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	port, count := 0, 0
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		key, value, ok := strings.Cut(lines.Text(), "=")
		if !ok || strings.TrimSpace(key) != "secure_admin_port" {
			continue
		}
		count++
		n, err := strconv.Atoi(strings.Trim(strings.TrimSpace(value), `"`))
		if err != nil || !validPort(n, false) {
			return 0
		}
		port = n
	}
	if lines.Err() != nil || count != 1 {
		return 0
	}
	return port
}

func (f *Front) secureContextURL(r *http.Request) string {
	port := f.cfg.PublicListen.HTTPS
	path := r.URL.Path
	query := ""
	if f.cfg.IngressPath != "" {
		port = f.cfg.DSMHTTPSPort
		// Match readDsmHttpsURL: enter the HTTPS desktop and reopen the App,
		// without forwarding credentials, callbacks or other URL parameters.
		path = "/webman/"
		query = url.Values{"launchApp": {"SYNO.SDS." + f.cfg.Package + ".Instance"}}.Encode()
	}
	if !validPort(port, false) || !validRequestAuthority(r) {
		return ""
	}
	origin, _ := url.Parse(requestOrigin(r))
	u := url.URL{Scheme: "https", Host: net.JoinHostPort(origin.Hostname(), strconv.Itoa(port)), Path: path, RawQuery: query}
	return u.String()
}

// The static page uses the SDK's DSM light palette, with no script or automatic
// navigation. Its title and refusal text match the shared identity catalogs.
const secureContextStyle = `:root{color-scheme:light;font:13px/1.55 Verdana,Arial,"Microsoft JhengHei","Microsoft YaHei",Meiryo,sans-serif;color:#414b55;background:#f4f6f3}body{margin:0;min-height:100vh;box-sizing:border-box;display:flex;align-items:center;justify-content:center;padding:24px}main{box-sizing:border-box;width:100%;max-width:440px;padding:40px 32px;text-align:center;background:#fff;border-radius:12px;box-shadow:0 1px 2px rgba(65,75,85,.08),0 12px 32px rgba(65,75,85,.14);overflow-wrap:anywhere}svg{width:56px;height:56px;margin-bottom:20px;color:#057feb}h1{margin:0 0 16px;font-size:20px;line-height:1.4}p{margin:0;color:rgba(65,75,85,.82)}p+p{margin-top:12px}footer{margin-top:24px}a{display:inline-flex;align-items:center;justify-content:center;min-height:36px;box-sizing:border-box;padding:7px 24px;border-radius:999px;background:#057feb;color:#fff;text-decoration:none}a:hover{background:#0075db}a:focus-visible{outline:2px solid #057feb;outline-offset:3px}@media(max-width:480px){body{padding:16px}main{padding:32px 24px}}`

func (f *Front) writeHTTPSRequired(w http.ResponseWriter, r *http.Request) {
	locale, messages := denialMessages(r)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Language", locale)
	w.Header().Add("Vary", "Accept-Language")
	w.Header().Add("Vary", "Sec-Fetch-Dest")
	w.Header().Add("Vary", "Sec-Fetch-Mode")
	w.Header().Add("Vary", "Accept")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// An iframe navigation is not a top-level document. Older clients without
	// Fetch Metadata keep the gateway's existing HTML Accept fallback.
	dest := r.Header.Get("Sec-Fetch-Dest")
	if !isNavigation(r) || dest != "" && dest != "document" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "https_required", "message_key": "gateway.https_required", "message": messages["https_required"]})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src "+cspHash(secureContextStyle)+"; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>%s</style><main aria-labelledby="https-title"><svg viewBox="0 0 48 48" aria-hidden="true"><rect x="10" y="21" width="28" height="22" rx="4" fill="currentColor"/><path d="M16 21v-8a8 8 0 0 1 16 0v8" fill="none" stroke="currentColor" stroke-width="4"/><circle cx="24" cy="31" r="3" fill="white"/></svg><h1 id="https-title" data-i18n="gateway.https_title">%s</h1><p role="alert" data-error-code="https_required" data-i18n="gateway.https_required">%s</p>`, locale, html.EscapeString(messages["https_title"]), secureContextStyle, html.EscapeString(messages["https_title"]), html.EscapeString(messages["https_required"]))
	if target := f.secureContextURL(r); target != "" {
		fmt.Fprintf(w, `<footer><a data-i18n="gateway.https_open" href="%s">%s</a></footer>`, html.EscapeString(target), html.EscapeString(messages["https_open"]))
	} else {
		fmt.Fprintf(w, `<p data-i18n="gateway.https_unknown">%s</p>`, html.EscapeString(messages["https_unknown"]))
	}
	fmt.Fprint(w, "</main></html>")
}
