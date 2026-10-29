package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
)

// AdministratorAdmitter verifies, without a session, that a named account is
// a current DSM administrator. dsm.EntryGate implements it from DSM's native
// role provider; the App does not supply the answer.
type AdministratorAdmitter interface {
	AdmitAdministrator(ctx context.Context, user string) (Identity, error)
}

// ErrInsideDispatch refuses InvokeAsAdministrator from CGI or from a request
// the server is handling: a request must never choose the account it runs as.
var ErrInsideDispatch = errors.New("api: InvokeAsAdministrator runs only in a package process outside HTTP dispatch")

type dispatchKey struct{}

// InvokeAsAdministrator runs one App JSON route, in process, for user after
// the entry authenticator verifies that user is a current DSM administrator.
// It is for package-account commands outside CGI that already authenticated
// the person who asked, such as a helper daemon's subcommand. It refuses under
// CGI and inside any request this SDK dispatches, refuses integration, HTTP,
// stream and SDK routes, and applies the route's permission and entry mode as
// a request would. Params must be one JSON value within the server's limit.
func (s *Server) InvokeAsAdministrator(ctx context.Context, user, method string, params json.RawMessage) (out any, err error) {
	if os.Getenv("GATEWAY_INTERFACE") != "" || ctx.Value(dispatchKey{}) != nil {
		return nil, ErrInsideDispatch
	}
	route, known := s.routes[method]
	if _, owned := sdkPolicy(method); !known || owned || route.policy == policyIntegration || route.handler == nil && route.requestHandler == nil {
		return nil, &Error{"unknown_method", "unknown method"}
	}
	if int64(len(params)) > s.maxRequestBytes || !json.Valid(params) {
		return nil, BadRequest("invalid params")
	}
	admitter, ok := s.auth.(AdministratorAdmitter)
	if !ok {
		return nil, errors.New("entry authenticator cannot verify an administrator without a session")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultRequestTimeout)
		defer cancel()
	}
	id, err := admitter.AdmitAdministrator(ctx, user)
	if err != nil {
		return nil, err
	}
	if !id.Admin || id.User != user {
		return nil, ErrAdminRequired
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("handler panic: %v", p)
			out, err = nil, internalError
		}
	}()
	return route.Invoke(context.WithValue(ctx, dispatchKey{}, true), Caller{identity: id, authenticated: true}, nil, params)
}
