package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/identityroute"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
)

type request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func decode(raw []byte, into any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func reply(w http.ResponseWriter, data any, code string) {
	out := map[string]any{"success": code == ""}
	if code == "" {
		out["data"] = data
	} else {
		out["error"] = map[string]string{"code": code, "message": "Sign-in could not be completed."}
	}
	raw, _ := json.Marshal(out)
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", strconv.Itoa(len(raw)))
	w.WriteHeader(200)
	_, _ = w.Write(raw)
}

// Routes registers identity operations under the central immutable SDK policy.
func (s *Service) Routes() []api.Route {
	routes := []api.Route{}
	for _, method := range []string{"identity.status", "identity.login", "identity.session", "identity.logout"} {
		name := method
		routes = append(routes, api.SDKIdentityRoute(api.HandleResponse(name, func(_ context.Context, _ api.Caller, w http.ResponseWriter, r *http.Request, p json.RawMessage) error {
			if len(p) == 0 {
				p = []byte(`{}`)
			}
			s.handle(w, r, request{Method: name, Params: p})
			return nil
		}), identityroute.New()))
	}
	return routes
}

type identityHandler struct {
	http.Handler
	next http.Handler
}

func (h *identityHandler) Unwrap() http.Handler { return h.next }

// Wrap revalidates App identity before business JSON and raw HTTP requests.
// Identity operations use the same central policy engine with no App methods.
func (s *Service) Wrap(next http.Handler) http.Handler {
	identityServer, err := api.NewServerWithPermissions(s, []byte(`{"schema":1,"default":"administrators","methods":{}}`), s.Routes()...)
	if err != nil {
		panic(err)
	}
	return &identityHandler{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer io.Copy(io.Discard, r.Body)
		if e := api.CheckOrigin(r); e != nil {
			reply(w, nil, e.Code)
			return
		}
		if method := api.HTTPRouteMethod(next, r); method != "" {
			if s.guard(w, r, method) {
				next.ServeHTTP(w, r)
			}
			return
		}
		if r.URL.Query().Get("method") != "" {
			if e := api.PrepareTransferRequest(w, r); e != nil {
				reply(w, nil, e.Code)
				return
			}
			if !s.guard(w, r, r.URL.Query().Get("method")) {
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != "POST" {
			reply(w, nil, "method_not_allowed")
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			reply(w, nil, "json_required")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			reply(w, nil, "request_too_large")
			return
		}
		var req request
		if decode(raw, &req) != nil || req.Method == "" {
			reply(w, nil, "bad_request")
			return
		}
		if len(req.Params) == 0 {
			req.Params = []byte(`{}`)
		}
		if strings.HasPrefix(req.Method, "identity.") {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			identityServer.ServeHTTP(w, r)
			return
		}
		if !s.guard(w, r, req.Method) {
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		next.ServeHTTP(w, r)
	}), next: next}
}
func (s *Service) guard(w http.ResponseWriter, r *http.Request, method string) bool {
	// Every business request rechecks identity: no developer-maintained list of
	// sensitive method names can accidentally omit a write operation.
	var token string
	var rec record
	revalidate := method != "app.status" && method != "greeting.get"
	err := s.transaction(func(st *state) error { var e error; token, rec, e = s.current(st, r, revalidate); return e })
	if err != nil {
		code := "session_required"
		if errors.Is(err, errRevalidation) {
			code = "session_revalidation_required"
			if strings.HasPrefix(method, "nas.") || method == "bg.browse" || method == "bg.jobs.submit" {
				code = "dsm_session_required"
			}
		}
		reply(w, nil, code)
		return false
	}
	if revalidate {
		s.cookie(w, token, rec.Expires)
	}

	return true
}
func (s *Service) handle(w http.ResponseWriter, r *http.Request, req request) {
	if req.Method == "identity.status" {
		if decode(req.Params, &struct{}{}) != nil {
			reply(w, nil, "bad_request")
			return
		}
		reply(w, map[string]bool{"enabled": true}, "")
		return
	}
	if req.Method == "identity.login" && r.TLS == nil {
		reply(w, nil, "https_required")
		return
	}
	var token string
	var rec record
	code := ""
	err := s.transaction(func(st *state) error {
		switch req.Method {
		case "identity.login":
			var in struct {
				Account  string `json:"account"`
				Password string `json:"password"`
				OTP      string `json:"otp"`
			}
			if decode(req.Params, &in) != nil || in.Account == "" || in.Password == "" || len(in.Account) > 1024 || len(in.Password) > 4096 || len(in.OTP) > 128 {
				code = "bad_request"
				return nil
			}
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			if s.limited(st, ip, strings.ToLower(in.Account)) {
				code = "rate_limited"
				return nil
			}
			sub, c := s.login(r.Context(), in.Account, in.Password, in.OTP)
			code = c
			if code != "" {
				return nil
			}
			if len(st.Sessions) >= 4096 {
				code = "auth_unavailable"
				return nil
			}
			// Rotation also removes the caller's previous session.
			if old, err := r.Cookie(s.cookieName); err == nil {
				delete(st.Sessions, digest(old.Value))
			}
			token, err = randomID()
			if err != nil {
				return err
			}
			rec = record{Subject: sub, Expires: s.cfg.Now().Add(s.cfg.TTL)}
			st.Sessions[digest(token)] = rec
		case "identity.session":
			var in struct {
				Bootstrap bool `json:"bootstrap"`
			}
			if decode(req.Params, &in) != nil {
				code = "bad_request"
				return nil
			}
			_, hasDSM := r.Cookie("id")
			var e error
			token, rec, e = s.current(st, r, hasDSM == nil)
			// Explicit bootstrap only when there is no application cookie. An expired
			// application session must return to the login surface before reissuing.
			_, appErr := r.Cookie(s.cookieName)
			if e != nil && in.Bootstrap && appErr != nil {
				if r.TLS == nil {
					code = "https_required"
					return nil
				}
				_, err := r.Cookie("id")
				if err != nil {
					code = "session_required"
					return nil
				}
				sub, err := s.verify(r)
				if err != nil {
					code = "session_required"
					return nil
				}
				if len(st.Sessions) >= 4096 {
					code = "auth_unavailable"
					return nil
				}
				token, err = randomID()
				if err != nil {
					return err
				}
				rec = record{Subject: sub, Expires: s.cfg.Now().Add(s.cfg.TTL)}
				st.Sessions[digest(token)] = rec
				e = nil
			}
			if e != nil {
				code = "session_required"
			}
		case "identity.logout":
			if decode(req.Params, &struct{}{}) != nil {
				code = "bad_request"
				return nil
			}
			if old, err := r.Cookie(s.cookieName); err == nil {
				delete(st.Sessions, digest(old.Value))
			}
		default:
			code = "unknown_method"
		}
		return nil
	})
	if err != nil {
		reply(w, nil, "auth_unavailable")
		return
	}
	if code != "" {
		if code == "session_required" {
			s.cookie(w, "", rec.Expires)
		}
		reply(w, nil, code)
		return
	}
	if req.Method == "identity.logout" {
		s.cookie(w, "", rec.Expires)
		reply(w, map[string]bool{"loggedOut": true}, "")
		return
	}
	s.cookie(w, token, rec.Expires)
	reply(w, map[string]any{"user": rec.Subject.User, "uid": rec.Subject.UID, "expiresAt": rec.Expires}, "")
}
