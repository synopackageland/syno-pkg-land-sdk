package idgateway

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// BrowserConfig starts the upstream's native OIDC flow on document entry and
// SPA navigation to a native login page. No upstream credentials are inspected.
type BrowserConfig struct {
	Start         string   `json:"start"`
	LoginPaths    []string `json:"login_paths"`
	CallbackPaths []string `json:"callback_paths"`
	// CompletionQuery marks a native login page that is finishing authentication.
	// Only presence is checked; the value belongs exclusively to the upstream.
	CompletionQuery string `json:"completion_query,omitempty"`
}

func browserPath(p string) bool {
	return len(p) > 0 && len(p) <= 512 && strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "/_sso") && !strings.ContainsAny(p, "%?#\r\n\t ") && cleanPath(p)
}
func (b BrowserConfig) validate() error {
	u, err := url.Parse(b.Start)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || !browserPath(u.Path) || strings.Contains(b.Start, "%") || len(b.Start) > 1024 || len(b.LoginPaths) == 0 || len(b.LoginPaths) > 16 || len(b.CallbackPaths) == 0 || len(b.CallbackPaths) > 16 {
		return errors.New("oidc.browser needs a fixed local start and login/callback paths outside /_sso")
	}
	for _, p := range append(append([]string{}, b.LoginPaths...), b.CallbackPaths...) {
		if !browserPath(p) {
			return errors.New("invalid oidc.browser path")
		}
	}
	if b.CompletionQuery != "" && !namePattern.MatchString(b.CompletionQuery) {
		return errors.New("invalid oidc.browser completion_query")
	}
	return nil
}

//go:embed browser.js
var browserScript string

func (f *Front) serveBrowser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	cfg, _ := json.Marshal(struct {
		*BrowserConfig
		IngressPath string `json:"ingress_path,omitempty"`
	}{f.cfg.Browser, f.cfg.IngressPath})
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	io.WriteString(w, "(function(cfg){\n"+browserScript+"\n})("+string(cfg)+");")
}

func (f *Front) injectBrowser(res *http.Response) error {
	if f.cfg.Browser == nil || !isNavigation(res.Request) || res.StatusCode != 200 || !strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/html") || res.Header.Get("Content-Disposition") != "" {
		return nil
	}
	if enc := res.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		return errors.New("OIDC browser HTML must use identity encoding")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxRewriteBody+1))
	if err != nil {
		return err
	}
	if len(raw) > maxRewriteBody {
		return errors.New("OIDC browser HTML exceeds rewrite limit")
	}
	res.Body.Close()
	// Insert before any upstream script; never alter its CSP or read its state.
	lower := bytes.ToLower(raw)
	at := bytes.Index(lower, []byte("<head>"))
	if at < 0 {
		return errors.New("OIDC browser HTML has no head element")
	}
	at += len("<head>")
	tag := []byte(`<script src="` + f.route("/_sso/browser.js") + `"></script>`)
	out := make([]byte, 0, len(raw)+len(tag))
	out = append(out, raw[:at]...)
	out = append(out, tag...)
	out = append(out, raw[at:]...)
	res.Body = io.NopCloser(bytes.NewReader(out))
	res.ContentLength = int64(len(out))
	res.Header.Set("Content-Length", strconv.Itoa(len(out)))
	res.Header.Set("Cache-Control", "no-store")
	for _, key := range []string{"ETag", "Last-Modified", "Content-MD5", "Digest"} {
		res.Header.Del(key)
	}
	return nil
}
