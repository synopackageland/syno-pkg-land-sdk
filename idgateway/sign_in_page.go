package idgateway

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Keep the same DSM tokens and controls as the SDK's standalone launcher,
// with a compact, centred App identity. CSP pins the complete inline sheet.
const denialStyle = menuStyle + `
main{text-align:center;max-width:440px;padding:40px 32px}
.brand{display:flex;align-items:center;flex-direction:column;gap:16px;margin-bottom:16px}
.app-icon{display:block;width:64px;height:64px;margin:0;object-fit:contain}
h1{margin:0;font-size:20px}footer{justify-content:center}
a.primary{display:inline-flex;align-items:center;justify-content:center;min-height:36px;min-width:128px;box-sizing:border-box;padding:7px 24px;border-radius:999px;text-decoration:none;font:inherit;line-height:1.35;transition:background-color .15s}
a.primary:focus-visible{outline:2px solid var(--primary);outline-offset:3px}
@media(max-width:480px){body{padding:16px;align-items:center}main{padding:32px 24px}}
`

func loginLocation(app, resume string) string {
	param, _ := json.Marshal(map[string]string{"ssoResume": resume})
	return "/?" + url.Values{"launchApp": {"SYNO.SDS." + app + ".Instance"}, "launchParam": {string(param)}}.Encode()
}

type appBrand struct{ name, icon string }

// Only local package metadata is read, once at startup. The image is inlined
// so the signed-out failure page never fetches a protected App/DSM asset.
func readAppBrand(root, pkg string) appBrand {
	brand := appBrand{name: pkg}
	if !namePattern.MatchString(pkg) {
		return brand
	}
	base := filepath.Join(root, pkg)
	if f, err := os.Open(filepath.Join(base, "INFO")); err == nil {
		scanner := bufio.NewScanner(io.LimitReader(f, 64<<10))
		for scanner.Scan() {
			key, value, ok := strings.Cut(scanner.Text(), "=")
			if ok && key == "displayname" {
				brand.name = strings.Trim(value, `"`)
				break
			}
		}
		f.Close()
	}
	icons, _ := filepath.Glob(filepath.Join(base, "target", "ui", "images", "app_*_72.png"))
	if len(icons) != 0 {
		if f, err := os.Open(icons[0]); err == nil {
			raw, err := io.ReadAll(io.LimitReader(f, (512<<10)+1))
			f.Close()
			if err == nil && len(raw) <= 512<<10 && len(raw) >= 8 && string(raw[:8]) == "\x89PNG\r\n\x1a\n" {
				brand.icon = "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
			}
		}
	}
	return brand
}
