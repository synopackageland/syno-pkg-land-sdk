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
// offers to open the address. Success shows briefly before the page is
// replaced. It is constant, so the CSP pins its hash.
const menuProbeScript = `(function(){var d=document.documentElement.dataset,o=document.getElementById("opening"),u=document.getElementById("unreachable"),c=new AbortController(),k=setTimeout(function(){c.abort()},8000);function fail(){clearTimeout(k);d.state="fail";o.hidden=true;u.hidden=false}d.state="checking";fetch(d.ping,{mode:"cors",credentials:"omit",cache:"no-store",redirect:"error",signal:c.signal}).then(function(r){return r.ok?r.json():null}).then(function(j){if(!j||j.package!==d.package)return fail();clearTimeout(k);d.state="ok";setTimeout(function(){location.replace(d.target)},typeof matchMedia=="function"&&matchMedia("(prefers-reduced-motion: reduce)").matches?0:400)},fail)})();`

func cspHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

var menuProbeHash = cspHash(menuProbeScript)

// menuStyle copies the DSM look of @synopackageland/ui as static CSS: the page
// cannot load the package. The root's data-state (start, checking, ok, fail),
// data-hop (router, else a cloud) and data-mark (x, pause, lock) drive the
// illustration. It is constant, so the CSP pins its hash.
const menuStyle = `:root{color-scheme:light dark;--bg:#f4f6f3;--card:#fff;--text:#414b55;--text2:rgba(65,75,85,.82);--muted:rgba(65,75,85,.6);--line:#cbd0d6;--tile:#eef2f6;--primary:#057feb;--hover:#0075db;--active:#0062b8;--soft:rgba(5,127,235,.1);--ctl:rgba(190,200,192,.26);--ctl-hover:rgba(190,200,192,.5);--ok:#009e05;--bad:#e64040;--warn:#f58414;--shadow:0 1px 2px rgba(65,75,85,.08),0 12px 32px rgba(65,75,85,.14)}
@media (prefers-color-scheme:dark){:root{--bg:#121513;--card:#1b1f1c;--text:#e3e8e4;--text2:rgba(227,232,228,.74);--muted:rgba(227,232,228,.5);--line:rgba(200,214,204,.26);--tile:#242925;--hover:#3597ef;--active:#0062b8;--soft:rgba(5,127,235,.16);--ctl:rgba(200,214,204,.09);--ctl-hover:rgba(200,214,204,.16);--ok:#6fdc8a;--bad:#f59a9a;--warn:#f5b26b;--shadow:0 1px 2px rgba(0,0,0,.4),0 12px 32px rgba(0,0,0,.4)}}
body{margin:0;min-height:100vh;box-sizing:border-box;display:flex;align-items:center;justify-content:center;padding:24px;background:var(--bg);color:var(--text);font:13px/1.55 Verdana,Arial,"Microsoft JhengHei","Microsoft YaHei",Meiryo,sans-serif}
main{position:relative;box-sizing:border-box;width:100%;max-width:520px;padding:28px 32px 24px;overflow:hidden;overflow-wrap:anywhere;background:var(--card);border-radius:8px;box-shadow:var(--shadow);animation:in .25s ease-out both}
main:before{content:"";position:absolute;inset:0 0 auto;height:3px;background:linear-gradient(90deg,transparent,var(--primary),transparent) no-repeat;background-size:40% 100%;opacity:0}
[data-state=checking] main:before{opacity:1;animation:bar 1.2s ease-in-out infinite}
section{animation:in .25s ease-out both}[hidden],[data-state=start] .js{display:none!important}
h1{margin:0 0 6px;font-size:17px;font-weight:600;line-height:1.4}p{margin:0;color:var(--text2)}.hint{margin-top:8px;font-size:12px;color:var(--muted)}
footer{display:flex;flex-wrap:wrap;justify-content:flex-end;gap:8px;margin-top:24px}form{display:contents}
.btn{display:inline-flex;align-items:center;justify-content:center;box-sizing:border-box;min-width:96px;min-height:32px;padding:5px 16px;border:0;border-radius:999px;background:var(--ctl);color:var(--text);font:inherit;line-height:1.35;text-align:center;text-decoration:none;cursor:pointer;transition:background-color .15s}
.btn:hover{background:var(--ctl-hover)}.btn:focus-visible{outline:2px solid var(--primary);outline-offset:2px}
.primary{min-width:120px;background:var(--primary);color:#fff}.primary:hover{background:var(--hover)}.primary:active{background:var(--active)}
.ghost{min-width:0;margin-inline-end:auto;padding-inline:8px;background:none;color:var(--primary)}.ghost:hover{background:var(--soft)}
svg{display:block;width:100%;max-width:304px;height:auto;margin:0 auto 20px}
.node,.nas{fill:var(--tile)}.bay{fill:var(--card)}.led{fill:var(--ok)}
.g{fill:none;stroke:var(--text2);stroke-width:1.6;stroke-linecap:round;stroke-linejoin:round}.gf{fill:var(--text2)}
.ln{fill:none;stroke:var(--primary);stroke-width:2;stroke-linecap:round}.app{stroke:var(--line);stroke-dasharray:4 5}
.ep{fill:var(--card);stroke:var(--primary);stroke-width:1.5}.ep.a{stroke:var(--line)}.pulse{fill:none;stroke:var(--primary);opacity:0;transform-box:fill-box;transform-origin:center}
[data-state=checking] .app{stroke:var(--primary);animation:flow .9s linear infinite}[data-state=checking] .pulse{animation:pulse 1.4s ease-out infinite}
[data-state=ok] .app{stroke:var(--primary);stroke-dasharray:none}[data-state=ok] .ep.a{stroke:var(--primary);fill:var(--soft)}
[data-state=fail] .app{stroke:var(--bad)}[data-mark=pause] .app,[data-mark=lock] .dsm{stroke:var(--warn)}[data-mark=lock] .dsm{stroke-dasharray:4 5}[data-mark=lock] .app{stroke:var(--line)}
.mk,.router,[data-hop=router] .cloud{display:none}[data-hop=router] .router{display:inline}
[data-state=ok] .ok,[data-state=fail][data-mark=x] .x,[data-state=fail][data-mark=pause] .pause,[data-state=fail][data-mark=lock] .lock{display:inline;animation:pop .3s ease-out both}
.mk circle{fill:var(--bad)}.ok circle{fill:var(--ok)}.pause circle,.lock circle{fill:var(--warn)}.mk path{fill:none;stroke:var(--card);stroke-width:2;stroke-linecap:round;stroke-linejoin:round}.mk,.pop{transform-box:fill-box;transform-origin:center}
@keyframes in{from{opacity:0;transform:translateY(6px)}}@keyframes bar{from{background-position:-40% 0}to{background-position:140% 0}}@keyframes flow{to{stroke-dashoffset:-18}}@keyframes pulse{from{opacity:.6;transform:scale(1)}to{opacity:0;transform:scale(1.6)}}@keyframes pop{from{opacity:0;transform:scale(.4)}}
@media (max-width:480px){body{align-items:flex-start;padding:16px}main{padding:24px 20px 20px}footer{flex-direction:column-reverse}.btn{width:100%}.ghost{margin:0}}
@media (prefers-reduced-motion:reduce){*,:before{animation:none!important;transition:none!important}}`

var menuStyleHash = cspHash(menuStyle)

// menuArt shows the browser, the hop that reached DSM (a router, else a cloud)
// and the NAS, whose DSM answers while the App path is checked, open, broken,
// paused or waiting for sign-in. It holds no text, so it suits every language.
const menuArt = `<svg viewBox="0 0 320 120" aria-hidden="true" focusable="false">` +
	`<circle class="node" cx="40" cy="60" r="26"/><rect class="g" x="28" y="49" width="24" height="15" rx="2"/><path class="g" d="M23 68h34"/>` +
	`<path class="ln" d="M66 60h44"/><circle class="node" cx="136" cy="60" r="26"/>` +
	`<path class="g cloud" d="M127 68h19a6 6 0 0 0 .6-12a8.5 8.5 0 0 0-16.4-1.6A6.8 6.8 0 0 0 127 68z"/>` +
	`<g class="router"><rect class="g" x="122" y="60" width="28" height="10" rx="2"/><path class="g" d="M129 60l-3-9M143 60l3-9"/><circle class="gf" cx="128" cy="65" r="1.3"/><circle class="gf" cx="133" cy="65" r="1.3"/></g>` +
	`<rect class="nas" x="200" y="8" width="104" height="104" rx="12"/>` +
	`<rect class="bay" x="252" y="22" width="40" height="12" rx="2"/><rect class="bay" x="252" y="40" width="40" height="12" rx="2"/><rect class="bay" x="252" y="68" width="40" height="12" rx="2"/><rect class="bay" x="252" y="86" width="40" height="12" rx="2"/>` +
	`<circle class="led" cx="286" cy="28" r="1.6"/><circle class="led" cx="286" cy="46" r="1.6"/><circle class="led" cx="286" cy="74" r="1.6"/><circle class="led" cx="286" cy="92" r="1.6"/>` +
	`<path class="ln dsm" d="M162 60C186 60 192 38 214 38"/><path class="ln app" d="M162 60C186 60 192 82 214 82"/>` +
	`<circle class="ep" cx="228" cy="38" r="14"/><path class="gf" d="M222 32h5v5h-5zM229 32h5v5h-5zM222 39h5v5h-5zM229 39h5v5h-5z"/>` +
	`<circle class="pulse" cx="228" cy="82" r="14"/><circle class="ep a" cx="228" cy="82" r="14"/><rect class="g" x="222" y="76" width="12" height="12" rx="3.5"/><circle class="gf" cx="228" cy="82" r="2"/>` +
	`<g class="mk x" transform="translate(189 71)"><circle r="9"/><path d="M-3.2-3.2l6.4 6.4m0-6.4l-6.4 6.4"/></g>` +
	`<g class="mk pause" transform="translate(189 71)"><circle r="9"/><path d="M-2.5-3.5v7m5-7v7"/></g>` +
	`<g class="mk ok" transform="translate(189 71)"><circle r="9"/><path d="M-3.5 0l2.5 2.5 4.5-5"/></g>` +
	`<g class="mk lock" transform="translate(189 49)"><circle r="9"/><path d="M-3.5-.5h7v5h-7zM-2-.5v-2a2 2 0 0 1 4 0v2"/></g></svg>`

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

// menuButton is a link. A ghost button leads the footer; the last other one
// is the primary action unless Try again is.
type menuButton struct {
	href, key string
	ghost     bool
}

// menuPage is one explanation: what happened and what an administrator can
// do. Ordinary users get no provider, address or approval details. mark picks
// the broken hop of the illustration: x, pause or lock.
type menuPage struct {
	title, text, hint string
	buttons           []menuButton
	mark              string
	retryPrimary      bool
}

func menuExplanation(cfg Config, d MenuDecision, m map[string]string, admin, connect bool) menuPage {
	provider := strings.NewReplacer("{provider}", menuProviderNames[d.Provider])
	page := menuPage{title: m["menu_title"], text: m["menu_unavailable"], mark: "x"}
	if !admin {
		return page
	}
	connectButton := func() {
		if connect {
			page.buttons = append(page.buttons, menuButton{href: connectLink(cfg.Package, d.Provider), key: "menu_connect"})
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
		page.text, page.mark = provider.Replace(m["menu_paused"]), "pause"
		connectButton()
	case MenuQuickConnect:
		page.title, page.text = m["menu_quickconnect_title"], m["menu_quickconnect"]
		connectButton()
	case MenuSingleLocal:
		page.text = m["menu_single_local"]
		page.buttons = append(page.buttons, menuButton{href: "settings.cgi", key: "menu_settings"})
	}
	return page
}

func menuUnreachable(cfg Config, d MenuDecision, target string, m map[string]string, admin, connect bool) menuPage {
	page := menuPage{title: m["menu_unreachable_title"], text: m["menu_unreachable"], mark: "x", buttons: []menuButton{{target, "menu_open_anyway", true}}}
	if d.Provider == ProviderDirect {
		// From outside the local network a router usually forwards DSM's
		// port only. Connect gives the App an address that works from here.
		page.title, page.text = m["menu_outside_title"], m["menu_unavailable"]
		if admin {
			page.text = m["menu_outside"]
			if cfg.External != nil && cfg.External.Connect == "none" {
				return page
			}
			if connect {
				page.buttons = append(page.buttons, menuButton{href: connectLink(cfg.Package, d.Provider), key: "menu_connect"})
			} else {
				page.hint = m["menu_connect_missing"]
			}
		}
		return page
	}
	if d.Provider == ProviderCanonical {
		page.text = m["menu_single_unreachable"]
		if u, err := url.Parse(d.Origin); err == nil && strings.HasSuffix(u.Hostname(), ".ts.net") {
			page.hint = m["menu_single_tailnet"]
		}
	}
	// The App may only be stopped or restarting: Try again stays primary.
	page.retryPrimary = true
	switch {
	case !admin:
	case d.Provider == ProviderCanonical || d.Provider == ProviderManual:
		page.buttons = append(page.buttons, menuButton{href: "settings.cgi", key: "menu_settings"})
	case connect:
		page.buttons = append(page.buttons, menuButton{href: connectLink(cfg.Package, d.Provider), key: "menu_connect"})
	}
	return page
}

func (p menuPage) html(id string, hidden bool, code string, m map[string]string) string {
	var b strings.Builder
	attrs := ""
	if hidden {
		attrs = " hidden"
	}
	fmt.Fprintf(&b, `<section id="%s" role="alert" aria-labelledby="%s-title"%s><h1 id="%s-title">%s</h1><p data-error-code="%s">%s</p>`, id, id, attrs, id, html.EscapeString(p.title), html.EscapeString(code), html.EscapeString(p.text))
	if p.hint != "" {
		fmt.Fprintf(&b, `<p class="hint">%s</p>`, html.EscapeString(p.hint))
	}
	link := func(button menuButton, class string) {
		fmt.Fprintf(&b, `<a class="btn%s" href="%s">%s</a>`, class, html.EscapeString(button.href), html.EscapeString(m[button.key]))
	}
	var actions []menuButton
	b.WriteString(`<footer>`)
	for _, button := range p.buttons {
		if button.ghost {
			link(button, " ghost")
		} else {
			actions = append(actions, button)
		}
	}
	retry := func(class string) {
		b.WriteString(`<form method="get"><button class="btn` + class + `" type="submit">` + html.EscapeString(m["menu_retry"]) + `</button></form>`)
	}
	if p.retryPrimary || len(actions) == 0 {
		for _, button := range actions {
			link(button, "")
		}
		retry(" primary")
	} else {
		retry("")
		for i, button := range actions {
			class := ""
			if i == len(actions)-1 {
				class = " primary"
			}
			link(button, class)
		}
	}
	b.WriteString(`</footer></section>`)
	return b.String()
}

// menuCSP allows the constant style and, when given, one constant script.
func menuCSP(script, connect string) string {
	csp := "default-src 'none'; "
	if script != "" {
		csp += "script-src " + script + "; connect-src " + connect + "; "
	}
	return csp + "style-src " + menuStyleHash + "; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
}

// menuHead opens the page and its card. The root attributes are package data
// and the illustration state; title is escaped here.
func menuHead(locale, attrs, title string) string {
	return fmt.Sprintf(`<!doctype html><html lang="%s"%s><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>%s</style><main>%s`, locale, attrs, html.EscapeString(title), menuStyle, menuArt)
}

// DSM's URL launcher sends cookies, not the SynoToken authenticate.cgi needs.
// Keep that carrier out of URLs: an unprivileged GET obtains it from DSM and
// posts it back to this same entry before the existing session check runs.
func writeMenuBootstrap(w http.ResponseWriter, r *http.Request) {
	locale, m := denialMessages(r)
	nonce := randomID(16)
	signIn := strings.TrimRight(m["sign_in_required"], ".。")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", locale)
	w.Header().Add("Vary", "Accept-Language")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; connect-src 'self'; style-src "+menuStyleHash+"; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	// No config, target, provider or projection is interpolated in this page.
	fmt.Fprintf(w, `%s<section><h1 id="status" role="status">%s</h1><p class="js" id="checking">%s</p><form id="session" method="post" action="?" hidden><input type="hidden" name="SynoToken" id="token"></form><footer id="retry" hidden><form method="get"><button class="btn primary" type="submit">%s</button></form></footer><noscript><p>%s</p></noscript></section></main><script nonce="%s">document.documentElement.dataset.state='checking';fetch('/webman/login.cgi?enable_syno_token=yes',{credentials:'same-origin',cache:'no-store'}).then(function(r){if(!r.ok)throw new Error();return r.json()}).then(function(j){if(typeof j.SynoToken!=='string'||!j.SynoToken)throw new Error();document.getElementById('token').value=j.SynoToken;document.getElementById('session').submit()}).catch(function(){var d=document.documentElement.dataset;d.state='fail';d.mark='lock';document.getElementById('status').textContent=%q;document.getElementById('checking').textContent=%q;document.getElementById('retry').hidden=false})</script></html>`, menuHead(locale, ` data-state="start"`, m["menu_title"]), html.EscapeString(m["menu_opening"]), html.EscapeString(m["menu_checking"]), html.EscapeString(m["menu_retry"]), html.EscapeString(signIn), nonce, m["sign_in"], signIn)
}

func prepareMenuPost(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" || r.Header.Get("Origin") != requestOrigin(r) {
		http.Error(w, "invalid entry", http.StatusForbidden)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if r.ParseForm() != nil || len(r.PostForm) != 1 || len(r.PostForm["SynoToken"]) != 1 || r.PostForm.Get("SynoToken") == "" {
		http.Error(w, "invalid entry", http.StatusForbidden)
		return false
	}
	r.Header.Set("X-SYNO-TOKEN", r.PostForm.Get("SynoToken"))
	return true
}

// writeMenuPage serves every launcher result other than the existing direct
// destination. It needs a DSM session with access to this App: a redirect
// opens a trusted address only after the probe, and explanations name the
// provider and offer Connect only to administrators.
func writeMenuPage(w http.ResponseWriter, r *http.Request, cfg Config, d MenuDecision, target string, auth api.Authenticator, connect bool) {
	if r.Method == http.MethodGet && r.Header.Get("Cookie") != "" && r.Header.Get("X-SYNO-TOKEN") == "" {
		writeMenuBootstrap(w, r)
		return
	}
	locale, m := denialMessages(r)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Language", locale)
	h.Add("Vary", "Accept-Language")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	csp := menuCSP("", "")
	var id api.Identity
	err := errors.New("no authenticator")
	if auth != nil {
		id, err = auth.Authenticate(r)
	}
	// The illustration shows a router for a direct connection from outside,
	// else a cloud. It names no provider.
	hop := ""
	if d.Provider == ProviderDirect {
		hop = ` data-hop="router"`
	}
	explain := func(page menuPage, code string) string {
		attrs := fmt.Sprintf(` data-state="fail" data-mark="%s"%s`, page.mark, hop)
		return menuHead(locale, attrs, page.title) + page.html("menu", false, code, m) + `</main></html>`
	}
	var body string
	switch {
	case errors.Is(err, api.ErrNoSession):
		page := menuPage{title: m["sign_in"], text: strings.TrimRight(m["sign_in_required"], ".。"), mark: "lock"}
		body = explain(page, "sign_in_required")
	case err != nil:
		body = explain(menuExplanation(cfg, d, m, false, false), "menu_unavailable")
	case d.Kind == MenuRedirect:
		csp = menuCSP(menuProbeHash, d.Origin)
		attrs := fmt.Sprintf(` data-state="start" data-mark="x"%s data-ping="%s" data-package="%s" data-target="%s"`, hop, html.EscapeString(d.Origin+PingPath), html.EscapeString(cfg.Package), html.EscapeString(target))
		page := menuUnreachable(cfg, d, target, m, id.Admin, connect)
		code := "menu_unreachable"
		if d.Provider == ProviderDirect {
			code = "menu_outside"
		}
		body = menuHead(locale, attrs, m["menu_opening"]) +
			fmt.Sprintf(`<section id="opening" role="status"><h1>%s</h1><p class="js">%s</p><noscript><footer><a class="btn primary" href="%s">%s</a></footer></noscript></section>`, html.EscapeString(m["menu_opening"]), html.EscapeString(m["menu_checking"]), html.EscapeString(target), html.EscapeString(m["menu_continue"])) +
			page.html("unreachable", true, code, m) + `</main><script>` + menuProbeScript + `</script></html>`
	default:
		page := menuExplanation(cfg, d, m, id.Admin, connect)
		code := "menu_unavailable"
		if id.Admin {
			code = "menu_" + d.Reason
		}
		body = explain(page, code)
	}
	h.Set("Content-Security-Policy", csp)
	fmt.Fprint(w, body)
}
