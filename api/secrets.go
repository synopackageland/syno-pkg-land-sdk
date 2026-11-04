package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/synopackageland/syno-pkg-land-sdk/secret"
)

// WithRequestSecrets declares credential field names anywhere within a JSON
// route's params. Common credential names are always included. Use secret.Value
// in decoded Go params too. The handler runs in a request-only scope, SDK
// background stores refuse that scope. Results and public errors are unchanged.
// Use ordinary JSON handlers; grouped/raw/stream handlers cannot provide this
// contract and are refused at construction.
func WithRequestSecrets(route Route, fields ...string) Route {
	if route.batch != nil || (route.handler == nil && route.requestHandler == nil) {
		panic("request secrets require an ungrouped JSON handler")
	}
	original := route
	invoke := func(ctx context.Context, c Caller, r *http.Request, params json.RawMessage) (any, error) {
		redactor := secret.NewRedactor(params, fields...)
		redactor.IncludeRequestCredentials(r)
		return original.Invoke(redactor.WithScope(ctx), c, r, params)
	}
	route.handler = nil
	route.requestHandler = invoke
	return route
}
