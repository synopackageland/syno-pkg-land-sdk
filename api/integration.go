package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
)

// IntegrationPermission names the permissions.json value for a fixed
// service-to-App callback. It is valid only for HandleIntegration routes, and
// the CLI requires each one in dsm-package.yaml integrations and lists it on
// the approval screen.
const IntegrationPermission = "integration"

// HandleIntegration registers a raw HTTP callback that DSM services or other
// systems call without a DSM session, selected by exactly one ?method=<method>
// query value so existing callback URLs keep working. It is the HandleHTTP raw
// route with one difference: the SDK skips the DSM session and App entry gate
// for this method only, so h must verify the caller itself (token, HMAC, key
// or signed locator) before acting. Browser-originated requests still pass
// CheckOrigin unless the route opts into AllowCrossSiteNavigation. h may act
// for a DSM account only through ActAs.
func HandleIntegration(method string, h http.Handler) Route {
	return Route{method: method, httpHandler: h, integration: true}
}

// AllowCrossSiteNavigation lets a HandleIntegration route receive top-level
// browser navigations that another site or no page started: a payment
// provider's return redirect, its form POST back, or a link opened from mail
// or a chat message. Such a navigation is a GET or HEAD, or a POST whose body
// is a form (application/x-www-form-urlencoded or multipart/form-data); when
// the browser sends Sec-Fetch-Mode or Sec-Fetch-Dest they must be "navigate"
// and "document". The SDK removes the Cookie, X-SYNO-TOKEN and SynoToken
// headers before h runs, so an admitted cross-site request never carries an
// ambient DSM or App session; h must verify the request itself, as for any
// integration call. Same-origin requests are unchanged and keep their
// headers. Any other cross-site request is still refused with cross_origin.
func (r Route) AllowCrossSiteNavigation() Route {
	r.navigation = true
	return r
}

// ErrNotIntegration means ActAs ran outside an integration handler.
var ErrNotIntegration = errors.New("api.ActAs is only available inside a HandleIntegration handler")

// ErrAccountDisabled is an ActAs refusal: the DSM account is disabled or expired.
var ErrAccountDisabled = errors.New("DSM account is disabled or expired")

// PrincipalAdmitter applies the App entry gate to a named DSM account without
// a session. dsm.EntryGate implements it; ActAs refuses any other authenticator.
type PrincipalAdmitter interface {
	AdmitPrincipal(ctx context.Context, user string) (Identity, error)
}

type integrationKey struct{}

type integrationScope struct {
	auth  Authenticator
	entry string
}

// ActAs returns a non-administrator Caller for user after the App entry gate
// admits that account: it must exist, be enabled and unexpired, and DSM
// Application Privileges must allow it. Only an integration handler may call
// it, after its own verification has bound the request to user; ctx or r must
// come from that handler's request. The Caller never carries DSM administrator
// authority, even for an administrator.
func ActAs(ctx context.Context, r *http.Request, user string) (Caller, error) {
	scope, ok := ctx.Value(integrationKey{}).(integrationScope)
	if !ok && r != nil {
		scope, ok = r.Context().Value(integrationKey{}).(integrationScope)
	}
	if !ok {
		return Caller{}, ErrNotIntegration
	}
	if scope.entry == "admin-only" {
		return Caller{}, ErrAdminRequired
	}
	admitter, ok := scope.auth.(PrincipalAdmitter)
	if !ok {
		return Caller{}, errors.New("entry authenticator cannot admit an account without a session")
	}
	id, err := admitter.AdmitPrincipal(ctx, user)
	if err != nil {
		return Caller{}, err
	}
	if id.User != user {
		return Caller{}, ErrAppPrivilegeDenied
	}
	return Caller{identity: Identity{User: id.User}, authenticated: true}, nil
}

// integrationMethod is the single ?method= value naming an integration route.
// A request naming one with a repeated or unparsable method is refused.
func (s *Server) integrationMethod(r *http.Request) (string, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	for _, method := range q["method"] {
		if route, ok := s.routes[method]; ok && route.integration {
			return method, err == nil && len(q["method"]) == 1
		}
	}
	return "", true
}

func (s *Server) serveIntegration(w http.ResponseWriter, r *http.Request) bool {
	method, single := s.integrationMethod(r)
	if method == "" {
		return false
	}
	if !single {
		writeStreamError(w, BadRequest("integration requests name one method"))
		return true
	}
	// Services post without browser headers; a browser's request must still be
	// same-origin, unless the route accepts cross-site navigations, which then
	// lose every ambient credential.
	if r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		if e := CheckOrigin(r); e != nil {
			if !s.routes[method].navigation || !topLevelNavigation(r) {
				writeStreamError(w, e)
				return true
			}
			r = withoutAmbientCredentials(r)
		}
	}
	noteMethod(r, method)
	ctx := context.WithValue(r.Context(), integrationKey{}, integrationScope{s.auth, s.entry})
	s.routes[method].httpHandler.ServeHTTP(w, r.WithContext(ctx))
	return true
}

// topLevelNavigation reports whether r looks like a browser loading a page:
// a GET or HEAD, or a form POST, and, when the browser says so, a navigate
// request for a document. A page cannot set the Sec-Fetch-* headers.
func topLevelNavigation(r *http.Request) bool {
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" && mode != "navigate" {
		return false
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return true
	case http.MethodPost:
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		return err == nil && (media == "application/x-www-form-urlencoded" || media == "multipart/form-data")
	}
	return false
}

// withoutAmbientCredentials returns r with the browser's cookies and DSM
// tokens removed, so a cross-site navigation cannot act as a signed-in user.
func withoutAmbientCredentials(r *http.Request) *http.Request {
	r = r.Clone(r.Context())
	for _, name := range []string{"Cookie", "X-SYNO-TOKEN", "SynoToken"} {
		r.Header.Del(name)
	}
	return r
}

// IntegrationRoute returns the integration method a request selects, or "".
// Middleware uses it to pass the request to Server without a session check.
func (s *Server) IntegrationRoute(r *http.Request) string {
	method, _ := s.integrationMethod(r)
	return method
}

// IntegrationRouteMethod finds an integration registration through SDK
// middleware. It does not verify the request; the integration handler does.
func IntegrationRouteMethod(handler http.Handler, r *http.Request) string {
	for range 16 {
		if router, ok := handler.(interface{ IntegrationRoute(*http.Request) string }); ok {
			return router.IntegrationRoute(r)
		}
		wrapper, ok := handler.(interface{ Unwrap() http.Handler })
		if !ok {
			return ""
		}
		handler = wrapper.Unwrap()
	}
	return ""
}
