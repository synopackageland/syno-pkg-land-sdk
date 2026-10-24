package idgateway

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// ConnectPackage is NAS Remote Connect, whose App URLs page gives Apps their
// external addresses.
const ConnectPackage = "SynoPkgConnect"

// menuProbeScript checks that the trusted target answers /_spl/ping for this
// package before opening it. Slow NASes and provider restarts take seconds, so
// it waits up to 8 seconds; any failure shows the explanation, which still
// offers to open the address. It is constant, so the CSP pins its hash.
const menuProbeScript = `(function(){var d=document.documentElement.dataset,o=document.getElementById("opening"),u=document.getElementById("unreachable"),s=setTimeout(function(){o.hidden=false},300),c=new AbortController(),k=setTimeout(function(){c.abort()},8000);function fail(){clearTimeout(s);clearTimeout(k);o.hidden=true;u.hidden=false}fetch(d.ping,{mode:"cors",credentials:"omit",cache:"no-store",redirect:"error",signal:c.signal}).then(function(r){return r.ok?r.json():null}).then(function(j){if(!j||j.package!==d.package)return fail();clearTimeout(k);location.replace(d.target)},fail)})();`

var menuProbeHash = func() string {
	sum := sha256.Sum256([]byte(menuProbeScript))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

const menuStyle = `:root{color-scheme:light dark}body{font:16px system-ui;margin:0;padding:24px;line-height:1.6}main{max-width:640px;margin:0 auto}.box{box-sizing:border-box;margin:24px 0;padding:24px;border:1px solid GrayText;border-radius:12px;overflow-wrap:anywhere}[hidden]{display:none}h1{font-size:1.25rem;margin-top:0}footer{display:flex;flex-wrap:wrap;gap:12px}button,.button{font:inherit;padding:8px 16px;cursor:pointer;border:1px solid ButtonBorder;border-radius:4px;background:ButtonFace;color:ButtonText;text-decoration:none}`

// menuProviderNames are product names, the same in every language.
var menuProviderNames = map[string]string{ProviderCloudflare: "Cloudflare", ProviderTailscale: "Tailscale", ProviderWorkers: "workers.dev"}

// menuConnectScopes map a provider to the scope Connect's App URLs page selects.
var menuConnectScopes = map[string]string{ProviderCloudflare: "public", ProviderTailscale: "private", ProviderWorkers: "workers"}

func connectInstalled() bool {
	_, err := os.Stat("/var/packages/" + ConnectPackage + "/INFO")
	return err == nil
}

// connectLink opens Connect's window on this DSM origin with the App, and the
// provider's scope, preselected. DSM decodes launchParam and then each value,
// so the route is encoded twice. Connect only preselects; it changes nothing.
func connectLink(pkg, provider string) string {
	route := "/apps/" + pkg
	if scope := menuConnectScopes[provider]; scope != "" {
		route += "/" + scope
	}
	return "/?launchApp=SYNO.SDS." + ConnectPackage + ".Instance&launchParam=" + url.QueryEscape("route="+url.QueryEscape(route))
}

type menuButton struct{ href, key string }

// menuPage is one explanation box: what happened and what an administrator
// can do. Ordinary users get no provider, address or approval details.
type menuPage struct {
	title, text, hint string
	buttons           []menuButton
}

func menuExplanation(cfg Config, d MenuDecision, m map[string]string, admin, connect bool) menuPage {
	provider := strings.NewReplacer("{provider}", menuProviderNames[d.Provider])
	page := menuPage{title: m["menu_title"], text: m["menu_unavailable"]}
	if !admin {
		return page
	}
	connectButton := func() {
		if connect {
			page.buttons = append(page.buttons, menuButton{connectLink(cfg.Package, d.Provider), "menu_connect"})
		} else {
			page.hint = m["menu_connect_missing"]
		}
	}
	switch d.Reason {
	case MenuNotEnabled:
		page.text = provider.Replace(m["menu_not_enabled"])
		page.hint = m["menu_not_enabled_hint"]
		connectButton()
	case MenuPaused:
		page.text = provider.Replace(m["menu_paused"])
		connectButton()
	case MenuQuickConnect:
		page.title, page.text = m["menu_quickconnect_title"], m["menu_quickconnect"]
		connectButton()
	case MenuSingleLocal:
		page.text = m["menu_single_local"]
		page.buttons = append(page.buttons, menuButton{"settings.cgi", "menu_settings"})
	}
	return page
}

func menuUnreachable(cfg Config, d MenuDecision, target string, m map[string]string, admin, connect bool) menuPage {
	page := menuPage{title: m["menu_unreachable_title"], text: m["menu_unreachable"]}
	if d.Provider == ProviderCanonical {
		page.text = m["menu_single_unreachable"]
		if u, err := url.Parse(d.Origin); err == nil && strings.HasSuffix(u.Hostname(), ".ts.net") {
			page.hint = m["menu_single_tailnet"]
		}
	}
	page.buttons = []menuButton{{target, "menu_open_anyway"}}
	switch {
	case !admin:
	case d.Provider == ProviderCanonical || d.Provider == ProviderManual:
		page.buttons = append(page.buttons, menuButton{"settings.cgi", "menu_settings"})
	case connect:
		page.buttons = append(page.buttons, menuButton{connectLink(cfg.Package, d.Provider), "menu_connect"})
	}
	return page
}

func (p menuPage) html(id string, hidden bool, code string, m map[string]string) string {
	var b strings.Builder
	attrs := ""
	if hidden {
		attrs = " hidden"
	}
	fmt.Fprintf(&b, `<section class="box" id="%s" role="alert" aria-labelledby="%s-title"%s><h1 id="%s-title">%s</h1><p data-error-code="%s">%s</p>`, id, id, attrs, id, html.EscapeString(p.title), html.EscapeString(code), html.EscapeString(p.text))
	if p.hint != "" {
		fmt.Fprintf(&b, `<p>%s</p>`, html.EscapeString(p.hint))
	}
	b.WriteString(`<footer><form method="get"><button type="submit">` + html.EscapeString(m["menu_retry"]) + `</button></form>`)
	for _, button := range p.buttons {
		fmt.Fprintf(&b, `<a class="button" href="%s">%s</a>`, html.EscapeString(button.href), html.EscapeString(m[button.key]))
	}
	b.WriteString(`</footer></section>`)
	return b.String()
}

// writeMenuPage serves every launcher result other than the existing direct
// destination. It needs a DSM session with access to this App: a redirect
// opens a trusted address only after the probe, and explanations name the
// provider and offer Connect only to administrators.
func writeMenuPage(w http.ResponseWriter, r *http.Request, cfg Config, d MenuDecision, target string, auth api.Authenticator, connect bool) {
	locale, m := denialMessages(r)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Language", locale)
	h.Add("Vary", "Accept-Language")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	csp := "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
	var id api.Identity
	err := errors.New("no authenticator")
	if auth != nil {
		id, err = auth.Authenticate(r)
	}
	head := fmt.Sprintf(`<!doctype html><html lang="%s"%%s><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%%s</title><style>%s</style><main>`, locale, menuStyle)
	var body string
	switch {
	case errors.Is(err, api.ErrNoSession):
		page := menuPage{title: m["menu_title"], text: strings.TrimRight(m["sign_in_required"], ".。")}
		body = fmt.Sprintf(head, "", html.EscapeString(page.title)) + page.html("menu", false, "sign_in_required", m)
	case err != nil:
		page := menuExplanation(cfg, d, m, false, false)
		body = fmt.Sprintf(head, "", html.EscapeString(page.title)) + page.html("menu", false, "menu_unavailable", m)
	case d.Kind == MenuRedirect:
		csp = "default-src 'none'; script-src " + menuProbeHash + "; connect-src " + d.Origin + "; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
		data := fmt.Sprintf(` data-ping="%s" data-package="%s" data-target="%s"`, html.EscapeString(d.Origin+PingPath), html.EscapeString(cfg.Package), html.EscapeString(target))
		page := menuUnreachable(cfg, d, target, m, id.Admin, connect)
		body = fmt.Sprintf(head, data, html.EscapeString(m["menu_opening"])) +
			fmt.Sprintf(`<p id="opening" role="status" hidden>%s</p><noscript><p><a href="%s">%s</a></p></noscript>`, html.EscapeString(m["menu_opening"]), html.EscapeString(target), html.EscapeString(m["menu_continue"])) +
			page.html("unreachable", true, "menu_unreachable", m) + `</main><script>` + menuProbeScript + `</script></html>`
	default:
		page := menuExplanation(cfg, d, m, id.Admin, connect)
		code := "menu_unavailable"
		if id.Admin {
			code = "menu_" + d.Reason
		}
		body = fmt.Sprintf(head, "", html.EscapeString(page.title)) + page.html("menu", false, code, m)
	}
	if !strings.HasSuffix(body, "</html>") {
		body += `</main></html>`
	}
	h.Set("Content-Security-Policy", csp)
	fmt.Fprint(w, body)
}
