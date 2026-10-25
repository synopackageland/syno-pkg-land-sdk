package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/jsonlimits"
)

// MaxRequestBytes is the default bound on the request body the CGI reads
// (CONTENT_LENGTH is caller-controlled).
const MaxRequestBytes = 1 << 20

// MaxRequestBytesCeiling is the most an App can declare with SetMaxRequestBytes.
const MaxRequestBytesCeiling = 16 << 20

// DefaultRequestTimeout bounds cooperative CGI work even after a browser
// disconnect: net/http/cgi does not add a deadline. The SDK's browser client
// waits two seconds longer (TS clientTimeout), so the reply arrives first. An
// App with longer or shorter operations sets its own with Timeout.
const DefaultRequestTimeout = 60 * time.Second

type deadlineOptions struct{ timeout time.Duration }

// DeadlineOption configures WithRequestDeadline.
type DeadlineOption func(*deadlineOptions)

// Timeout sets the request deadline. The App's browser client must wait
// longer than this, or it gives up before the reply arrives.
func Timeout(d time.Duration) DeadlineOption {
	return func(o *deadlineOptions) { o.timeout = d }
}

// WithRequestDeadline wraps the complete CGI handler, including middleware
// that runs before Server, in one deadline (DefaultRequestTimeout unless
// Timeout sets another). Server keeps a deadline it finds; it applies
// DefaultRequestTimeout only to a request that has none. A timeout that is not
// positive panics, so the mistake fails at startup.
func WithRequestDeadline(next http.Handler, opts ...DeadlineOption) http.Handler {
	o := deadlineOptions{timeout: DefaultRequestTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	if o.timeout <= 0 {
		panic("api.Timeout must be positive")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), o.timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ErrNoSession means the request carries no valid DSM session.
var ErrNoSession = errors.New("no valid DSM session")

// ErrAdminRequired is an entry refusal: the app admits only administrators.
var ErrAdminRequired = errors.New("app entry requires an administrator")

// ErrAppPrivilegeDenied is an entry refusal: DSM Application Privileges do not
// allow this account to use the app from this address.
var ErrAppPrivilegeDenied = errors.New("DSM Application Privileges deny this app")

// Authenticator verifies the DSM session behind a request.
type Authenticator interface {
	Authenticate(r *http.Request) (Identity, error)
}

// Server is the http.Handler behind the package CGI.
type Server struct {
	auth            Authenticator
	routes          map[string]Route
	maxRequestBytes int64
}

// NewServerWithPermissions builds the CGI server from the App's trusted
// permissions.json, never from request parameters. It refuses an SDK policy
// other than the one the package build validated, an empty authenticator
// and any route that is not usable, so a mistake fails at startup instead of
// opening a route.
func NewServerWithPermissions(auth Authenticator, raw []byte, routes ...Route) (*Server, error) {
	if err := checkSDKPolicy(); err != nil {
		return nil, err
	}
	p, err := parsePermissions(raw)
	if err != nil {
		return nil, err
	}
	return newServer(auth, p, routes...)
}

func newServer(auth Authenticator, p permissions, routes ...Route) (*Server, error) {
	if auth == nil {
		return nil, errors.New("api.NewServerWithPermissions needs an Authenticator")
	}
	registry, err := buildRegistry(routes)
	if err != nil {
		return nil, err
	}
	for method := range p.methods {
		if _, owned := sdkPolicy(method); owned {
			return nil, errors.New("App policy cannot override SDK route: " + method)
		}
		if _, known := registry[method]; !known {
			return nil, errors.New("permission configured for unregistered API method: " + method)
		}
	}
	for method, route := range registry {
		route.policy = p.forMethod(method)
		if rule, owned := sdkPolicy(method); owned {
			route.policy = rule
		}
		registry[method] = route
	}
	return &Server{auth: auth, routes: registry, maxRequestBytes: MaxRequestBytes}, nil
}

// SetMaxRequestBytes declares the App's JSON request body limit, from 1 byte
// to MaxRequestBytesCeiling. The JSON structure limits of jsonlimits still apply.
func (s *Server) SetMaxRequestBytes(n int64) error {
	if n < 1 || n > MaxRequestBytesCeiling {
		return fmt.Errorf("request body limit must be 1 to %d bytes", MaxRequestBytesCeiling)
	}
	s.maxRequestBytes = n
	return nil
}

type request struct {
	Version int             `json:"v,omitempty"`
	Calls   []BatchCall     `json:"calls,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := r.Context(), context.CancelFunc(func() {})
	if _, ok := ctx.Deadline(); !ok {
		ctx, cancel = context.WithTimeout(ctx, DefaultRequestTimeout)
	}
	defer cancel()
	r = r.WithContext(ctx)
	// DSM's CGI gateway cuts the reply short if the CGI exits with request
	// bytes unread, so drain whatever the handling below did not consume.
	defer io.Copy(io.Discard, r.Body)
	writeFailure := writeError
	if r.URL.Query().Get("method") != "" {
		transfer := &transferResponse{ResponseWriter: w}
		w = transfer
		writeFailure = func(_ http.ResponseWriter, err error) {
			if transfer.started {
				log.Printf("transfer failed after response started: %v", err)
				return
			}
			writeStreamError(transfer, err)
		}
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("handler panic: %v", p)
			writeFailure(w, internalError)
		}
	}()
	if method := r.URL.Query().Get("method"); method != "" {
		if e := CheckOrigin(r); e != nil {
			writeFailure(w, e)
			return
		}
		if e := PrepareTransferRequest(w, r); e != nil {
			writeFailure(w, e)
			return
		}
		caller, route, e := s.authorize(r, method)
		if e != nil {
			writeFailure(w, e)
			return
		}
		if route.streamHandler == nil {
			writeFailure(w, BadRequest("not an HTTP transfer method"))
			return
		}
		if err := route.streamHandler(r.Context(), caller, w, r); err != nil {
			writeFailure(w, asRequestError(ctx, err))
		}
		return
	}
	req, apiErr := readRequest(w, r, s.maxRequestBytes)
	if apiErr != nil {
		writeFailure(w, apiErr)
		return
	}
	if req.Calls != nil {
		s.serveBatch(w, r, req.Calls)
		return
	}
	caller, route, apiErr := s.authorize(r, req.Method)
	if apiErr != nil {
		writeFailure(w, apiErr)
		return
	}
	if route.streamHandler != nil {
		writeFailure(w, BadRequest("use HTTP transfer endpoint"))
		return
	}
	if route.responseHandler != nil {
		if err := route.responseHandler(r.Context(), caller, w, r, req.Params); err != nil {
			writeFailure(w, asRequestError(ctx, err))
		}
		return
	}
	var data any
	var err error
	if route.requestHandler != nil {
		data, err = route.requestHandler(r.Context(), caller, r, req.Params)
	} else {
		data, err = route.handler(r.Context(), caller, req.Params)
	}
	if err != nil {
		writeFailure(w, asRequestError(ctx, err))
		return
	}
	WriteData(w, data)
}

// WriteData answers as a JSON method does, {"success":true,"data":…}, for a
// stream handler whose reply is JSON.
func WriteData(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// PrepareTransferRequest forwards a native download form's token to the same
// authenticator as JSON requests. Raw upload bodies remain untouched.
func PrepareTransferRequest(w http.ResponseWriter, r *http.Request) *Error {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Method != http.MethodPost || media != "application/x-www-form-urlencoded" {
		return nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		return BadRequest("invalid transfer form")
	}
	tokens := r.PostForm["SynoToken"]
	if len(tokens) > 1 {
		return BadRequest("invalid transfer token")
	}
	if len(tokens) == 1 {
		token := tokens[0]
		if len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
			return BadRequest("invalid transfer token")
		}
		r.Header.Set("X-SYNO-TOKEN", token)
	}
	return nil
}

// authorize is the single access decision. Public routes skip authentication;
// everything else, including unknown methods, authenticates exactly once
// first, so anonymous callers cannot probe which methods exist.
func (s *Server) authorize(r *http.Request, method string) (Caller, Route, *Error) {
	route, known := s.routes[method]
	if known && route.policy == policyPublic {
		return Caller{}, route, nil
	}
	identity, err := s.auth.Authenticate(r)
	if r.Context().Err() != nil {
		return Caller{}, Route{}, asError(r.Context().Err())
	}
	if errors.Is(err, ErrNoSession) {
		return Caller{}, Route{}, &Error{"session_required", "sign in to DSM again"}
	}
	if errors.Is(err, ErrAdminRequired) {
		return Caller{}, Route{}, adminRequired
	}
	if errors.Is(err, ErrAppPrivilegeDenied) {
		return Caller{}, Route{}, &Error{"app_privilege_denied", "DSM Application Privileges do not allow this account to use this app"}
	}
	if err != nil {
		log.Printf("authentication unavailable: %v", err)
		return Caller{}, Route{}, &Error{"auth_unavailable", "DSM authentication is unavailable"}
	}
	// Signed-in callers learn that a method is unregistered; method names
	// already ship in the app's UI. Route constructors never grant permission.
	if !known {
		return Caller{}, Route{}, &Error{"unknown_method", "unknown method"}
	}
	if route.policy == policyAdmin && !identity.Admin {
		return Caller{}, Route{}, adminRequired
	}
	caller := Caller{identity: identity, authenticated: true}
	return caller, route, nil
}

func readRequest(w http.ResponseWriter, r *http.Request, limit int64) (request, *Error) {
	var req request
	if r.Method != http.MethodPost {
		return req, &Error{"method_not_allowed", "use POST"}
	}
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		return req, &Error{"json_required", "send Content-Type: application/json"}
	}
	if e := CheckOrigin(r); e != nil {
		return req, e
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil || !jsonlimits.WithinLimits(body) {
		return req, &Error{"request_too_large", "request body too large"}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() {
		return req, BadRequest(`body must be {"method": "...", "params": {...}}`)
	}
	if _, err := dec.Token(); err != io.EOF {
		return req, BadRequest(`body must be {"method": "...", "params": {...}}`)
	}
	// The decoder matches field names case-insensitively, so what it decoded
	// counts as present whatever the spelling.
	var keys map[string]json.RawMessage
	json.Unmarshal(body, &keys)
	_, hasCalls := keys["calls"]
	_, hasVersion := keys["v"]
	hasCalls = hasCalls || req.Calls != nil
	hasVersion = hasVersion || req.Version != 0
	if hasCalls {
		if req.Calls == nil || req.Version != 1 || req.Method != "" || req.Params != nil || len(req.Calls) < 1 || len(req.Calls) > MaxBatchMethods {
			return req, BadRequest("invalid batch envelope")
		}
		for _, call := range req.Calls {
			if !methodName.MatchString(call.Method) {
				return req, BadRequest("invalid batch method")
			}
		}
	} else if req.Method == "" || hasVersion {
		return req, BadRequest(`body must be {"method": "...", "params": {...}}`)
	}
	return req, nil
}

// CheckOrigin refuses requests another site initiated. DSM's SynoToken does
// not protect the CGI: with DSM's CSRF protection off every session shares one
// token DSM does not check. The JSON content type forces a CORS preflight,
// which DSM never answers; a form post or an upload needs none, so the server
// asks the browser where a request came from. Sec-Fetch-Site, which a page
// cannot set, decides when present: only "same-origin" passes, which also
// holds behind a proxy that rewrites Host. Without it, Origin, else Referer,
// must name the request's host (Host, or X-Forwarded-Host a reverse proxy
// set; a page cannot set either cross-site without the preflight). A request
// that changes state and proves no origin is refused; GET and HEAD downloads
// without proof pass.
func CheckOrigin(r *http.Request) *Error {
	crossOrigin := &Error{"cross_origin", "cross-origin request refused"}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		if site != "same-origin" {
			return crossOrigin
		}
		return nil
	}
	source := r.Header.Get("Origin")
	if source == "" {
		source = r.Header.Get("Referer")
	}
	if source == "" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			return nil
		}
		return &Error{"origin_required", "the browser did not say which page sent the request; open the app again in a current browser"}
	}
	u, err := url.Parse(source)
	if err != nil || u.Host == "" {
		return crossOrigin
	}
	forwarded, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Host"), ",")
	if u.Host != r.Host && (forwarded == "" || u.Host != strings.TrimSpace(forwarded)) {
		return crossOrigin
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(body); err != nil {
		// DSM replaces CGI 5xx bodies with HTML; preserve the API envelope.
		status = http.StatusOK
		payload.Reset()
		payload.WriteString(`{"success":false,"error":{"code":"internal","message":"Response unavailable"}}`)
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	// DSM's CGI bridge can terminate chunked responses without a final chunk.
	// Exact byte lengths frame successful and refused responses consistently.
	h.Set("Content-Length", strconv.Itoa(payload.Len()))
	w.WriteHeader(status)
	_, _ = w.Write(payload.Bytes())
}

func writeError(w http.ResponseWriter, err error) {
	e := asError(err)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": false,
		"error":   BatchError{Code: e.Code, Message: e.Message, Details: errorDetails(err)},
	})
}
