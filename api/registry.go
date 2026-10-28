// Package api is the package's one HTTP entry point. Every request passes the
// same transport guards and the centralized permissions.json policy before
// any handler runs. Unlisted methods require DSM administrators membership.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/identityroute"
)

type policy int

const (
	policyUnset policy = iota
	policyUser
	policyAdmin
	policyPublic
)

// Handler runs one business operation. It receives the caller the server
// verified and the raw `params` object; it decodes params itself.
type Handler func(ctx context.Context, caller Caller, params json.RawMessage) (any, error)

// Route binds a method name to its handler. Permissions come from JSON only.
type Route struct {
	batch           *BatchGroup
	method          string
	policy          policy
	handler         Handler
	requestHandler  RequestHandler
	responseHandler ResponseHandler
	streamHandler   StreamHandler
	ceremony        bool
}

// SDKIdentityRoute marks a fixed SDK sign-in ceremony route. Only SDK assembly
// can supply a valid internal key; App handlers with the same name stay gated.
func SDKIdentityRoute(route Route, key identityroute.Key) Route {
	route.ceremony = key.Valid() && identityCeremony(route.method)
	return route
}

// Handle registers an operation without scattering permissions among handlers.
func Handle(method string, h Handler) Route {
	return Route{method: method, handler: h}
}

// RequestHandler receives the original request after the normal transport and
// authentication guards. Credentials must remain request-scoped.
type RequestHandler func(context.Context, Caller, *http.Request, json.RawMessage) (any, error)

// HandleRequest registers an operation needing the authenticated HTTP request.
func HandleRequest(method string, h RequestHandler) Route {
	return Route{method: method, requestHandler: h}
}

// ResponseHandler can set cookies or stream a response after central authorization.
type ResponseHandler func(context.Context, Caller, http.ResponseWriter, *http.Request, json.RawMessage) error

func HandleResponse(method string, h ResponseHandler) Route {
	return Route{method: method, responseHandler: h}
}

// StreamHandler handles a query-selected HTTP transfer without reading JSON.
type StreamHandler func(context.Context, Caller, http.ResponseWriter, *http.Request) error

// HandleStream registers an HTTP transfer. Before the response starts, errors
// return a JSON envelope with an HTTP error status (see streamStatus). A handler
// must finish checks before writing; errors after headers/body start are logged
// without appending JSON to the transfer. JSON calls retain HTTP 200 envelopes.
func HandleStream(method string, h StreamHandler) Route {
	return Route{method: method, streamHandler: h}
}

// Invoke runs a JSON route's handler outside the HTTP dispatch, for a caller
// the server already verified: the bridge runtime answers an App's service
// request with the SDK's own route. Whoever invokes it decides who may.
func (r Route) Invoke(ctx context.Context, caller Caller, req *http.Request, params json.RawMessage) (any, error) {
	if expectedEntryMode != "" {
		if !caller.Authenticated() {
			return nil, &Error{"session_required", "sign in to DSM again"}
		}
		if expectedEntryMode == "admin-only" && !caller.Admin() {
			return nil, adminRequired
		}
	}
	switch {
	case r.handler != nil:
		return r.handler(ctx, caller, params)
	case r.requestHandler != nil:
		return r.requestHandler(ctx, caller, req, params)
	}
	return nil, fmt.Errorf("api: %s is not a JSON route", r.method)
}

// Identity is what an Authenticator established about the requester.
type Identity struct {
	User  string
	Admin bool
}

// Caller is the verified requester handed to handlers. Only the server sets
// its fields, so a handler cannot fabricate an authenticated caller; the zero
// value carries no authority.
type Caller struct {
	identity      Identity
	authenticated bool
}

func (c Caller) Authenticated() bool { return c.authenticated }
func (c Caller) User() string        { return c.identity.User }
func (c Caller) Admin() bool         { return c.authenticated && c.identity.Admin }

var adminRequired = &Error{"admin_required", "this action needs the DSM administrators group"}

// RequireAdmin reserves part of a shared route, such as global settings, for
// DSM administrators. Whole routes are reserved in permissions.json instead.
func RequireAdmin(c Caller) error {
	if !c.Admin() {
		return adminRequired
	}
	return nil
}

var methodName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

func buildRegistry(routes []Route) (map[string]Route, error) {
	registry := make(map[string]Route, len(routes))
	for _, r := range routes {
		if !methodName.MatchString(r.method) {
			return nil, fmt.Errorf("route %q: method must look like \"area.action\" (lowercase)", r.method)
		}
		if r.handler == nil && r.requestHandler == nil && r.responseHandler == nil && r.streamHandler == nil {
			return nil, fmt.Errorf("route %q has no handler", r.method)
		}
		if _, dup := registry[r.method]; dup {
			return nil, fmt.Errorf("route %q is registered twice", r.method)
		}
		registry[r.method] = r
	}
	return registry, nil
}

// Method is the route's method name, e.g. "greeting.set".
func (r Route) Method() string { return r.method }

// Public reports the centralized permission, not the constructor's name.
func (r Route) Public() bool { return r.declaredPermission() == policyPublic }

// AdminOnly reports the centralized permission. Unlisted routes default here.
func (r Route) AdminOnly() bool { return r.declaredPermission() == policyAdmin }
