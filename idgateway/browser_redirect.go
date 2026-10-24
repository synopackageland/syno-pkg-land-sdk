package idgateway

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// BrowserRedirectVersion lets builders verify source-override support.
const BrowserRedirectVersion = 2

// BrowserRedirectConfig accepts syntactically valid browser hosts, selecting the origin
// from each request. HTMLPaths opts exact upstream GET paths into bounded URL
// rewriting; Location and explicitly declared JSON fields already support it.
// Issuer identity and all server-to-server endpoints remain fixed on loopback.
type BrowserRedirectConfig struct {
	HTMLPaths []string `json:"html_paths,omitempty"`
	// SameOriginReferrer lets native forms carry a non-null Origin without
	// sending referrers to other origins. Gateway origin checks stay strict.
	SameOriginReferrer bool `json:"same_origin_referrer,omitempty"`
}

var browserDNSLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func browserHost(raw string) string {
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String()
	}
	if raw == "" || len(raw) > 253 {
		return ""
	}
	host := strings.ToLower(raw)
	for _, label := range strings.Split(host, ".") {
		if !browserDNSLabel.MatchString(label) {
			return ""
		}
	}
	return host
}

func (b *BrowserRedirectConfig) validate() error {
	if b == nil {
		return nil
	}
	if len(b.HTMLPaths) > 16 {
		return errors.New("oidc.browser_redirect permits at most 16 html_paths")
	}
	seen := map[string]bool{}
	for _, p := range b.HTMLPaths {
		if !browserPath(p) || seen[p] {
			return errors.New("oidc.browser_redirect html_paths must be unique exact local paths outside /_sso")
		}
		seen[p] = true
	}
	return nil
}

func (f *Front) browserAuthority(r *http.Request) bool {
	if f.external(r).Index(requestOrigin(r)) >= 0 {
		return true
	}
	b := f.cfg.BrowserRedirect
	if b == nil {
		return true
	}
	u, err := url.Parse(requestOrigin(r))
	if err != nil || !validRequestAuthority(r) {
		return false
	}
	host := browserHost(u.Hostname())
	if host == "" {
		return false
	}
	port := u.Port()
	expected := f.cfg.PublicListen.HTTP
	if r.TLS != nil {
		expected = f.cfg.PublicListen.HTTPS
		if port == "" {
			port = "443"
		}
	} else if port == "" {
		port = "80"
	}
	return expected != 0 && port == strconv.Itoa(expected)
}

func (f *Front) rewritesHTML(r *http.Request) bool {
	return f.cfg.BrowserRedirect != nil && r.Method == http.MethodGet && slices.Contains(f.cfg.BrowserRedirect.HTMLPaths, r.URL.Path)
}

func (f *Front) rewriteBrowserHTML(res *http.Response, origin string) error {
	if !f.rewritesHTML(res.Request) || res.StatusCode != http.StatusOK || res.Header.Get("Content-Disposition") != "" {
		return nil
	}
	typ, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || typ != "text/html" {
		return errors.New("OIDC redirect response must be text/html")
	}
	if enc := res.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		return errors.New("OIDC redirect HTML must use identity encoding")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxRewriteBody+1))
	if err != nil {
		return err
	}
	res.Body.Close()
	if len(raw) > maxRewriteBody {
		return errors.New("OIDC redirect HTML exceeds rewrite limit")
	}
	from, to := f.cfg.Issuer+"/authorize", origin+"/_sso/authorize"
	// Encodings in ordinary HTML/JS strings and encodeURIComponent / Python
	// quote. Match the exact endpoint boundary, never a lookalike path or host.
	encoders := []func(string) string{
		func(s string) string { return s },
		func(s string) string { return strings.ReplaceAll(s, "/", `\/`) },
		func(s string) string { return url.QueryEscape(s) },
		func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "%2F", "/") },
	}
	for _, encode := range encoders {
		old, new := encode(from), encode(to)
		for _, query := range []string{"?", "%3F", "%3f"} {
			raw = bytes.ReplaceAll(raw, []byte(old+query), []byte(new+query))
		}
		for _, end := range []string{`"`, "'", "<", ">", " ", "\n", "\r", "\t"} {
			raw = bytes.ReplaceAll(raw, []byte(old+end), []byte(new+end))
		}
	}
	res.Body = io.NopCloser(bytes.NewReader(raw))
	res.ContentLength = int64(len(raw))
	res.Header.Set("Content-Length", strconv.Itoa(len(raw)))
	uncacheBrowserResponse(res)
	if f.cfg.BrowserRedirect.SameOriginReferrer {
		res.Header.Set("Referrer-Policy", "same-origin")
	}
	return nil
}

// Per-request origins must never survive in shared caches or validators.
func uncacheBrowserResponse(res *http.Response) {
	res.Header.Set("Cache-Control", "no-store")
	for _, h := range []string{"ETag", "Last-Modified", "Content-MD5", "Digest", "Content-Digest", "Repr-Digest"} {
		res.Header.Del(h)
	}
}
