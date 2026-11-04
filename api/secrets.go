package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/synopackageland/syno-pkg-land-sdk/secret"
)

// WithRequestSecrets declares credential field names anywhere within a JSON
// route's params. Common credential names are always included. Use secret.Value
// in decoded Go params too. The handler runs in a request-only scope, SDK
// background stores refuse that scope, and results/public errors are redacted.
// Use ordinary JSON handlers; grouped/raw/stream handlers cannot provide this
// contract and are refused at construction.
func WithRequestSecrets(route Route, fields ...string) Route {
	if route.batch != nil || (route.handler == nil && route.requestHandler == nil) {
		panic("request secrets require an ungrouped JSON handler")
	}
	original := route
	invoke := func(ctx context.Context, c Caller, r *http.Request, params json.RawMessage) (any, error) {
		redactor := secret.NewRedactor(params, fields...)
		data, err := original.Invoke(redactor.WithScope(ctx), c, r, params)
		if err != nil {
			var public *Error
			if errors.As(err, &public) {
				safe := &Error{Code: redactor.Text(public.Code), Message: redactor.Text(public.Message)}
				if details := errorDetails(err); details != nil {
					return nil, WithDetails(safe, redactor.Redact(details))
				}
				return nil, safe
			}
			return nil, err
		}
		return redactor.Redact(data), nil
	}
	route.handler = nil
	route.requestHandler = invoke
	return route
}
