package api

import (
	"context"
	"errors"
	"log"
)

// Error is an API error the browser may see. Any other error a handler
// returns is logged and reported only as "internal". Errors travel in the
// JSON envelope with HTTP 200 for JSON calls, as DSM's own WebAPI does.
// HTTP transfers use the status documented on HandleStream; DSM nginx may
// replace a CGI error body, so clients must check the status before saving it.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// WithDetails attaches public, JSON-serializable fields to an API error.
// Do not include credentials, internal paths or the original I/O error here.
// Error keeps its two-field shape for existing callers.
func WithDetails(err *Error, details any) error { return &detailedError{err, details} }

type detailedError struct {
	err     *Error
	details any
}

func (e *detailedError) Error() string { return e.err.Error() }
func (e *detailedError) Unwrap() error { return e.err }

func errorDetails(err error) any {
	var detailed *detailedError
	if errors.As(err, &detailed) {
		return detailed.details
	}
	return nil
}

// BadRequest reports invalid params.
func BadRequest(message string) *Error {
	return &Error{"bad_request", message}
}

var internalError = &Error{"internal", "internal error"}

func asError(err error) *Error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{"request_timeout", "the request took longer than the App allows"}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{"request_canceled", "request canceled"}
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	log.Printf("handler error: %v", err)
	return internalError
}

func asRequestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return asError(ctx.Err())
	}
	return err
}
