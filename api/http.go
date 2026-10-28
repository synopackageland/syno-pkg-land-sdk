package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// HandleHTTP registers a fixed, full-segment path prefix. method names its
// permission in permissions.json; it is not callable through RPC or batches.
// The original URL and ResponseWriter reach h after origin, entry, identity
// and permission checks, preserving streaming and WebSocket interfaces.
func HandleHTTP(prefix, method string, h http.Handler) Route {
	return Route{method: method, httpPrefix: prefix, httpHandler: h}
}

// Bound by the CLI to the sorted HTTP registration inventory for this binary.
var expectedHTTPRoutesDigest string

type httpRegistration struct {
	Prefix string `json:"prefix"`
	Method string `json:"method"`
}

var httpPrefixPattern = regexp.MustCompile(`^/([A-Za-z0-9_-]+/)+$`)

func validateHTTPRoutes(registry map[string]Route) error {
	registrations := []httpRegistration{}
	hasApp := false
	for _, route := range registry {
		_, sdk := sdkPolicy(route.method)
		hasApp = hasApp || !sdk
		if route.httpPrefix == "" && route.httpHandler == nil {
			continue
		}
		prefix := route.httpPrefix
		if route.httpHandler == nil || !httpPrefixPattern.MatchString(prefix) || sdk {
			return fmt.Errorf("invalid HTTP route %q", prefix)
		}
		for _, reserved := range []string{"/_sso/", "/_spl/", "/api/", "/api.cgi/", "/rpc/", "/webman/", "/cgi/"} {
			if strings.HasPrefix(prefix, reserved) || strings.HasPrefix(reserved, prefix) {
				return fmt.Errorf("HTTP prefix %q overlaps a reserved path", prefix)
			}
		}
		for _, other := range registrations {
			if strings.HasPrefix(prefix, other.Prefix) || strings.HasPrefix(other.Prefix, prefix) {
				return errors.New("HTTP prefixes must not overlap")
			}
		}
		registrations = append(registrations, httpRegistration{prefix, route.method})
	}
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].Prefix < registrations[j].Prefix })
	raw, _ := json.Marshal(registrations)
	if hasApp && expectedHTTPRoutesDigest != "" && fmt.Sprintf("%x", sha256.Sum256(raw)) != expectedHTTPRoutesDigest {
		return errors.New("HTTP routes do not match package build policy")
	}
	return nil
}

type httpCallerKey struct{}

// HTTPCaller returns the verified identity inside a HandleHTTP handler.
func HTTPCaller(ctx context.Context) Caller {
	caller, _ := ctx.Value(httpCallerKey{}).(Caller)
	return caller
}

func (s *Server) serveHTTPRoute(w http.ResponseWriter, r *http.Request) bool {
	requestPath := rawHTTPPath(r)
	for method, route := range s.routes {
		if route.httpHandler == nil || !strings.HasPrefix(requestPath, route.httpPrefix) {
			continue
		}
		// Do not let a proxy interpret a different, decoded/cleaned path after
		// authorization. Prefix selection never consults caller query data.
		if r.URL.RawPath != "" || r.URL.EscapedPath() != r.URL.Path || strings.ContainsAny(requestPath, "\\\x00%") || path.Clean(requestPath)+"/" != strings.TrimSuffix(requestPath, "/")+"/" {
			writeStreamError(w, BadRequest("non-canonical HTTP route path"))
			return true
		}
		if e := CheckOrigin(r); e != nil {
			writeStreamError(w, e)
			return true
		}
		caller, _, e := s.authorize(r, method)
		if e != nil {
			writeStreamError(w, e)
			return true
		}
		// Keep identity mandatory even if another registry policy is added later.
		if !caller.Authenticated() {
			writeStreamError(w, &Error{"session_required", "sign in to DSM again"})
			return true
		}
		route.httpHandler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), httpCallerKey{}, caller)))
		return true
	}
	return false
}

// HTTPRoute returns the fixed permission key for a raw HTTP request, or "".
// Middleware uses it only to select transport handling; Server still makes
// the complete access decision before dispatch.
func (s *Server) HTTPRoute(r *http.Request) string {
	requestPath := rawHTTPPath(r)
	for _, route := range s.routes {
		if route.httpHandler != nil && strings.HasPrefix(requestPath, route.httpPrefix) {
			return route.method
		}
	}
	return ""
}

func rawHTTPPath(r *http.Request) string {
	if os.Getenv("GATEWAY_INTERFACE") == "" {
		return r.URL.Path
	}
	script := os.Getenv("SCRIPT_NAME")
	if strings.HasPrefix(script, "/") && strings.HasPrefix(r.URL.Path, script+"/") {
		return strings.TrimPrefix(r.URL.Path, script)
	}
	return ""
}

// HTTPRouteMethod finds the raw transport registration through SDK middleware.
// It does not authenticate the request or authorize its handler.
func HTTPRouteMethod(handler http.Handler, r *http.Request) string {
	for range 16 {
		if router, ok := handler.(interface{ HTTPRoute(*http.Request) string }); ok {
			return router.HTTPRoute(r)
		}
		wrapper, ok := handler.(interface{ Unwrap() http.Handler })
		if !ok {
			return ""
		}
		handler = wrapper.Unwrap()
	}
	return ""
}

type routeWrapper struct {
	http.Handler
	next http.Handler
}

func (h *routeWrapper) Unwrap() http.Handler { return h.next }
