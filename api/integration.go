package api

import (
	"context"
	"errors"
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
// CheckOrigin. h may act for a DSM account only through ActAs.
func HandleIntegration(method string, h http.Handler) Route {
	return Route{method: method, httpHandler: h, integration: true}
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
	// Services post without browser headers; a browser's request must still be same-origin.
	if r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		if e := CheckOrigin(r); e != nil {
			writeStreamError(w, e)
			return true
		}
	}
	ctx := context.WithValue(r.Context(), integrationKey{}, integrationScope{s.auth, s.entry})
	s.routes[method].httpHandler.ServeHTTP(w, r.WithContext(ctx))
	return true
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
