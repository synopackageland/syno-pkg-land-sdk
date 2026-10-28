package api

import (
	"bufio"
	"net"
	"net/http"
)

// transferResponse tracks when an HTTP status can no longer be changed.
// Unwrap keeps http.ResponseController available to streaming handlers.
type transferResponse struct {
	http.ResponseWriter
	started bool
}

func (w *transferResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *transferResponse) WriteHeader(status int) {
	if status >= 200 {
		w.started = true
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *transferResponse) Write(p []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(p)
}
func (w *transferResponse) FlushError() error {
	w.started = true
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *transferResponse) Flush() { _ = w.FlushError() }
func (w *transferResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.started = true
	}
	return conn, rw, err
}

func streamStatus(code string) int {
	switch code {
	case "not_found", "source_deleted", "unknown_method":
		return http.StatusNotFound
	case "forbidden", "permission", "permission_denied", "denied", "admin_required", "app_privilege_denied", "cross_origin", "origin_required":
		return http.StatusForbidden
	case "unauthorized", "session_required":
		return http.StatusUnauthorized
	case "bad_request":
		return http.StatusBadRequest
	case "request_too_large":
		return http.StatusRequestEntityTooLarge
	case "auth_unavailable", "unavailable", "service_unavailable", "request_timeout", "request_canceled":
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func writeStreamError(w http.ResponseWriter, err error) {
	e := asError(err)
	// Prepared file headers must not turn a JSON refusal into a saved attachment.
	for _, name := range []string{"Content-Disposition", "Content-Encoding", "Content-Range", "Accept-Ranges"} {
		w.Header().Del(name)
	}
	writeJSON(w, streamStatus(e.Code), map[string]any{
		"success": false,
		"error":   BatchError{Code: e.Code, Message: e.Message, Details: errorDetails(err)},
	})
}
