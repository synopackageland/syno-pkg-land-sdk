package idgateway

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
)

// PathIngressVersion is the CLI capability marker for DSM path-based gateways.
const PathIngressVersion = 2

type ingressPathKey struct{}

// ReservedIngressPrefixes includes DSM's native routes and package portal defaults.
// Installation also checks the NAS inventory; this list alone cannot establish availability.
var reservedIngressPrefixes = map[string]bool{
	"dsfile": true, "indexdb": true, "mobile": true, "oauth": true, "scripts": true,
	"sharing": true, "ssdp": true, "synosdsjslib": true, "synohdpack": true,
	"webapi": true, "webman": true, "calendar": true, "chatplus": true,
	"contacts": true, "drive": true, "file": true, "photo": true, "sso": true,
	"synologymeet": true, "synopkgland": true,
}
var customIngressPath = regexp.MustCompile(`^/(?:[a-z0-9]+(?:-[a-z0-9]+)*/)+$`)

func validateIngressPath(pkg, prefix string) error {
	if prefix == "" {
		return nil
	}
	if !namePattern.MatchString(pkg) {
		return errors.New("invalid ingress package")
	}
	if prefix == "/synopkgland/"+pkg+"/" {
		return nil
	}
	if len(prefix) > 255 || !customIngressPath.MatchString(prefix) {
		return errors.New("ingress_path requires /synopkgland/<exact-package-ID>/ or an unreserved lowercase path with leading and trailing slashes; / is unsupported")
	}
	for _, segment := range strings.Split(prefix, "/") {
		if len(segment) > 64 {
			return errors.New("ingress_path segments must be at most 64 characters")
		}
	}
	if reservedIngressPrefixes[strings.Split(prefix, "/")[1]] {
		return errors.New("ingress_path is reserved by DSM; select an unreserved path")
	}
	return nil
}

func ingressRoute(prefix, path string) string {
	if prefix == "" {
		return path
	}
	return strings.TrimSuffix(prefix, "/") + path
}
func ingressPaths(prefix string, paths []string) []string {
	if prefix == "" {
		return paths
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = ingressRoute(prefix, p)
	}
	return out
}
func (f *Front) route(path string) string { return ingressRoute(f.cfg.IngressPath, path) }
func requestCookiePath(r *http.Request) string {
	if path, ok := r.Context().Value(ingressPathKey{}).(string); ok && path != "" {
		return path
	}
	return "/"
}

// Manifest paths stay relative to the package namespace. The upstream receives
// the full path and must support this base path through its native settings.
// This deliberately does not rewrite arbitrary application HTML or JavaScript.
func scopeFront(c FrontConfig) FrontConfig {
	p := c.IngressPath
	if p == "" {
		return c
	}
	c.Passthrough = ingressPaths(p, c.Passthrough)
	c.UpstreamAuthPaths = ingressPaths(p, c.UpstreamAuthPaths)
	redirects := map[string]string{}
	for k, v := range c.Redirects {
		redirects[ingressRoute(p, k)] = ingressRoute(p, v)
	}
	c.Redirects = redirects
	optional := map[string]OptionalPassthroughRule{}
	for k, v := range c.OptionalPassthrough {
		v.Prefixes = ingressPaths(p, v.Prefixes)
		optional[k] = v
	}
	c.OptionalPassthrough = optional
	if c.Browser != nil {
		b := *c.Browser
		b.Start = ingressRoute(p, b.Start)
		b.LoginPaths = ingressPaths(p, b.LoginPaths)
		b.CallbackPaths = ingressPaths(p, b.CallbackPaths)
		c.Browser = &b
	}
	if c.BrowserRedirect != nil {
		b := *c.BrowserRedirect
		b.HTMLPaths = ingressPaths(p, b.HTMLPaths)
		c.BrowserRedirect = &b
	}
	c.RewriteJSON = append([]JSONRewrite(nil), c.RewriteJSON...)
	for i := range c.RewriteJSON {
		c.RewriteJSON[i].Path = ingressRoute(p, c.RewriteJSON[i].Path)
	}
	return c
}
