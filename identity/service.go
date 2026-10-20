// Package identity implements standalone identity only. It never supplies DSM
// credentials or file authority to business handlers.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
)

type Subject struct {
	User string `json:"user"`
	UID  string `json:"uid"`
}
type Config struct {
	DataDir       string
	CookiePath    string
	Endpoint      string
	Client        *http.Client
	TTL           time.Duration
	Now           func() time.Time
	Lookup        func(string) (Subject, error)
	Administrator func(context.Context, string) (bool, error)
}
type Service struct {
	cfg        Config
	dir        string
	cookieName string
	dsmAuth    api.Authenticator
}

func New(cfg Config) (*Service, error) {
	if cfg.DataDir == "" || !strings.HasPrefix(cfg.CookiePath, "/") || strings.ContainsAny(cfg.CookiePath, ";\r\n\\") {
		return nil, errors.New("invalid identity configuration")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "http://127.0.0.1:5000/webapi/entry.cgi"
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.TTL == 0 {
		cfg.TTL = 15 * time.Minute
	}
	if cfg.TTL < time.Second || cfg.TTL > 15*time.Minute {
		return nil, errors.New("identity TTL must be at most 15 minutes")
	}
	if cfg.Lookup == nil {
		cfg.Lookup = func(name string) (Subject, error) {
			u, err := user.Lookup(name)
			if err != nil {
				return Subject{}, err
			}
			return Subject{User: u.Username, UID: u.Uid}, nil
		}
	}
	if cfg.Administrator == nil {
		cfg.Administrator = dsm.IsAdministrator
	}
	// Names differ across application paths; cookies still remain host-only.
	return &Service{cfg: cfg, dir: filepath.Join(cfg.DataDir, "identity"), cookieName: "spl_session_" + digest(cfg.DataDir + "\x00" + cfg.CookiePath)[:16], dsmAuth: dsm.NewAuthenticator()}, nil
}
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func randomID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (s *Service) cookie(w http.ResponseWriter, token string, expires time.Time) {
	age := int(expires.Sub(s.cfg.Now()).Seconds())
	if age < 1 {
		age = 1
	}
	if token == "" {
		age = -1
		expires = time.Unix(1, 0)
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName, Value: token, Path: s.cfg.CookiePath, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: age})
}
func validSubject(sub Subject) bool {
	return sub.User != "" && sub.UID != "" && len(sub.User) <= 1024 && !strings.ContainsAny(sub.User, "\r\n\x00")
}
func (s *Service) resolve(name string) (Subject, error) {
	sub, err := s.cfg.Lookup(name)
	if err != nil || !validSubject(sub) || sub.User != name {
		return Subject{}, api.ErrNoSession
	}
	return sub, nil
}
func (s *Service) Authenticate(r *http.Request) (api.Identity, error) {
	var sub Subject
	err := s.transaction(func(st *state) error {
		_, rec, err := s.current(st, r, false)
		if err != nil {
			return err
		}
		sub = rec.Subject
		return nil
	})
	if err != nil {
		return api.Identity{}, api.ErrNoSession
	}
	admin, err := s.cfg.Administrator(r.Context(), sub.User)
	if err != nil {
		return api.Identity{}, errors.New("identity role lookup unavailable")
	}
	return api.Identity{User: sub.User, Admin: admin}, nil
}
func (s *Service) current(st *state, r *http.Request, revalidate bool) (string, record, error) {
	c, err := r.Cookie(s.cookieName)
	if err != nil || len(c.Value) != 64 {
		return "", record{}, api.ErrNoSession
	}
	key := digest(c.Value)
	rec, ok := st.Sessions[key]
	if !ok || !s.cfg.Now().Before(rec.Expires) {
		delete(st.Sessions, key)
		return "", record{}, api.ErrNoSession
	}
	sub, err := s.resolve(rec.Subject.User)
	if err != nil || sub != rec.Subject {
		delete(st.Sessions, key)
		return "", record{}, api.ErrNoSession
	}
	if revalidate {
		dc, err := r.Cookie("id")
		if err != nil || dc.Value == "" {
			return "", record{}, errRevalidation
		}
		live, err := s.verify(r)
		if err != nil || live != sub {
			delete(st.Sessions, key)
			return "", record{}, api.ErrNoSession
		}
		rec.Expires = s.cfg.Now().Add(s.cfg.TTL)
		st.Sessions[key] = rec
	}
	return c.Value, rec, nil
}

var errRevalidation = fmt.Errorf("session_revalidation_required")
