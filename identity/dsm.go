package identity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

type dsmReply struct {
	Success bool `json:"success"`
	Data    struct {
		Username string `json:"username"`
		SID      string `json:"sid"`
	} `json:"data"`
	Error struct {
		Code  int `json:"code"`
		Types []struct {
			Type string `json:"type"`
		} `json:"types"`
	} `json:"error"`
}

// dsmCall sends token only as X-SYNO-TOKEN: DSM web logins enable SynoToken,
// and their cookie alone is refused with 119.
func (s *Service) dsmCall(ctx context.Context, form url.Values, cookie, token string) (dsmReply, error) {
	var out dsmReply
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", s.cfg.Endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return out, errors.New("DSM unavailable")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "id", Value: cookie})
	}
	if token != "" {
		req.Header.Set("X-SYNO-TOKEN", token)
	}
	res, err := s.cfg.Client.Do(req)
	if err != nil {
		return out, errors.New("DSM unavailable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || res.StatusCode != 200 || json.Unmarshal(raw, &out) != nil {
		return out, errors.New("DSM unavailable")
	}
	return out, nil
}

// verify asks DSM's native authenticator with the original request carriers.
// Loopback HTTP would change the client address and invalidate IP-bound sessions.
func (s *Service) verify(r *http.Request) (Subject, error) {
	// Preserve the existing single-header validation, including cookie-only
	// sessions, while passing the original browser address to DSM.
	request := r.Clone(r.Context())
	request.Header.Set("X-SYNO-TOKEN", synoToken(r))
	id, err := s.dsmAuth.Authenticate(request)
	if err != nil {
		return Subject{}, errors.New("DSM session refused")
	}
	return s.resolve(id.User)
}

// synoToken returns the single, well-formed X-SYNO-TOKEN header or "".
func synoToken(r *http.Request) string {
	values := r.Header.Values("X-SYNO-TOKEN")
	if len(values) != 1 || len(values[0]) > 4096 || strings.ContainsFunc(values[0], unicode.IsControl) {
		return ""
	}
	return values[0]
}
func (s *Service) login(ctx context.Context, account, password, otp string) (Subject, string) {
	form := url.Values{"api": {"SYNO.API.Auth"}, "method": {"login"}, "version": {"7"}, "account": {account}, "passwd": {password}, "format": {"sid"}}
	if otp != "" {
		form.Set("otp_code", otp)
	}
	out, err := s.dsmCall(ctx, form, "", "")
	if err != nil {
		return Subject{}, "auth_unavailable"
	}
	if !out.Success {
		switch out.Error.Code {
		case 400:
			return Subject{}, "invalid_credentials"
		case 401:
			return Subject{}, "account_disabled"
		case 403:
			for _, kind := range out.Error.Types {
				if kind.Type == "otp" {
					return Subject{}, "otp_required"
				}
			}
		}
		return Subject{}, "login_not_permitted"
	}
	if out.Data.SID == "" {
		return Subject{}, "auth_unavailable"
	}
	// The temporary DSM session exists only to prove identity. Dispose of it
	// before issuing an application session, including when local lookup fails.
	loggedOut, err := s.dsmCall(context.WithoutCancel(ctx), url.Values{"api": {"SYNO.API.Auth"}, "method": {"logout"}, "version": {"7"}, "_sid": {out.Data.SID}}, "", "")
	if err != nil || !loggedOut.Success {
		return Subject{}, "auth_unavailable"
	}
	sub, err := s.resolve(account)
	if err != nil {
		return Subject{}, "invalid_credentials"
	}
	return sub, ""
}
