// Package media serves private assets to native audio/video elements without
// placing DSM credentials in URLs. See docs/media.md for wiring and limits.
package media

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
)

// Grant freshness is independent of the request's work deadline.
const DefaultTTL = 5 * time.Minute
const MaxTTL = 30 * time.Minute
const GrantParameter = "media_grant"

// Asset must already be opened as the package account, after owner checks.
// ETag is a quoted strong validator for these exact bytes (not the grant id).
type Asset struct {
	Content     io.ReadSeekCloser
	ContentType string
	ETag        string
}

// Config contains trusted App metadata and hooks; none comes from URL params.
type Config struct {
	DataDir string
	// Endpoint is the same-origin CGI or daemon ingress path, without query.
	Endpoint string
	// Entry identifies this App's DSM entry, e.g. SYNO.SDS.MyApp.Instance.
	Entry string
	// FreshAuth must verify live DSM session and App entry without caches.
	// Use dsm.NewMediaAuthenticator; normal RPC authentication is kept separate.
	FreshAuth  api.EntryAuthenticator
	Generation func(context.Context) (string, error)
	// Authorize checks owner/read access on every issue, renewal and transfer.
	// Return a stable revision; a changed revision invalidates existing grants.
	Authorize func(context.Context, api.Caller, string) (string, error)
	Open      func(context.Context, api.Caller, string) (Asset, error)
	// LookupUID defaults to native DSM LookupAccount (enabled accounts only).
	LookupUID func(context.Context, string) (string, error)
	TTL       time.Duration
	// Prefix names four App permissions: .issue, .renew, .revoke and .content.
	Prefix string
}

type Service struct {
	cfg         Config
	dir, cookie string
	aead        cipher.AEAD
	now         func() time.Time
}

type Grant struct {
	ID        string    `json:"media_grant"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
	TTLMS     int64     `json:"ttl_ms"`
}

type sealed struct {
	Secure  bool      `json:"secure"`
	Binding binding   `json:"binding"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

var idPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

func validID(id string) bool     { return idPattern.MatchString(id) }
func digest(value string) string { b := sha256.Sum256([]byte(value)); return hex.EncodeToString(b[:]) }
func apiBadID() error            { return api.BadRequest("invalid media grant") }
func denied() error {
	return &api.Error{Code: "permission_denied", Message: "media grant is unavailable; request a new URL"}
}

func New(cfg Config) (*Service, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host != "" || u.Scheme != "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || path.Clean(u.Path) != u.Path || !strings.HasPrefix(cfg.Endpoint, "/") || strings.ContainsAny(cfg.Endpoint, ";\\\r\n") || strings.HasPrefix(cfg.Endpoint, "//") {
		return nil, errors.New("media requires a same-origin endpoint path without query")
	}
	if cfg.DataDir == "" || cfg.Entry == "" || len(cfg.Entry) > 128 || cfg.FreshAuth == nil || cfg.Generation == nil || cfg.Authorize == nil || cfg.Open == nil {
		return nil, errors.New("media requires private storage, entry, fresh authenticator and asset hooks")
	}
	if cfg.FreshAuth.EntryMode() != "admin-only" && cfg.FreshAuth.EntryMode() != "app-privilege" {
		return nil, errors.New("media requires an explicit entry mode")
	}
	if cfg.TTL == 0 {
		cfg.TTL = DefaultTTL
	}
	if cfg.TTL < time.Second || cfg.TTL > MaxTTL {
		return nil, errors.New("media TTL must be at least one second and at most MaxTTL")
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "media"
	}
	if !prefixPattern.MatchString(cfg.Prefix) {
		return nil, errors.New("invalid media permission prefix")
	}
	if cfg.LookupUID == nil {
		cfg.LookupUID = func(ctx context.Context, user string) (string, error) {
			account, err := dsm.LookupAccount(ctx, user)
			if err != nil {
				return "", err
			}
			if account.Expired != "normal" {
				return "", api.ErrNoSession
			}
			return strconv.FormatUint(uint64(account.UID), 10), nil
		}
	}
	dir := filepath.Join(cfg.DataDir, "media")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 {
		return nil, errors.New("media storage must be a private directory")
	}
	key, err := loadKey(dir)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, dir: dir, cookie: "spl_media_" + digest(cfg.Entry + "\x00" + cfg.Endpoint)[:16], aead: aead, now: time.Now}, nil
}

// Authenticator wraps the existing RPC authenticator. The only headerless
// exception is the fixed GET/HEAD media selector, never another RPC/route.
func (s *Service) Authenticator(normal api.Authenticator) api.EntryAuthenticator {
	if normal == nil {
		panic("media needs the normal RPC authenticator")
	}
	if gate, ok := normal.(api.EntryAuthenticator); ok && gate.EntryMode() != s.cfg.FreshAuth.EntryMode() {
		panic("media entry mode differs from RPC entry")
	}
	return &authenticator{s: s, normal: normal}
}

type authenticator struct {
	s      *Service
	normal api.Authenticator
}

func (a *authenticator) EntryMode() string { return a.s.cfg.FreshAuth.EntryMode() }
func (a *authenticator) Authenticate(r *http.Request) (api.Identity, error) {
	if r.URL.Query().Get("method") != a.s.cfg.Prefix+".content" {
		return a.normal.Authenticate(r)
	}
	restored, err := a.s.restore(r)
	if err != nil {
		return api.Identity{}, api.ErrNoSession
	}
	return a.s.cfg.FreshAuth.Authenticate(restored)
}

func (s *Service) currentBinding(ctx context.Context, c api.Caller, r *http.Request) (binding, error) {
	if !c.Authenticated() {
		return binding{}, denied()
	}
	cookie, err := sessionCookie(r)
	if err != nil {
		return binding{}, err
	}
	uid, err := s.cfg.LookupUID(ctx, c.User())
	if err != nil || uid == "" {
		return binding{}, denied()
	}
	generation, err := s.cfg.Generation(ctx)
	if err != nil || generation == "" {
		return binding{}, denied()
	}
	return binding{User: c.User(), UID: uid, Entry: s.cfg.Entry, Generation: generation, Session: digest(cookie)}, nil
}
func sessionCookie(r *http.Request) (string, error) {
	// Reuse the SDK's exact duplicate/malformed cookie checks without accepting
	// a browser token from the query. This temporary value is never dispatched.
	copy := r.Clone(r.Context())
	copy.Header.Set("X-SYNO-TOKEN", "media-cookie-validation")
	copy.Header.Del("SynoToken")
	auth, err := api.DSMSession(copy)
	if err != nil {
		return "", denied()
	}
	return auth["dsm_cookie"], nil
}
func (s *Service) restore(r *http.Request) (*http.Request, error) {
	q, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil || len(q["method"]) != 1 || len(q[GrantParameter]) != 1 || len(q) != 2 || !validID(q.Get(GrantParameter)) {
		return nil, denied()
	}
	seal, err := s.unseal(r)
	if err != nil {
		return nil, err
	}
	cookie, err := sessionCookie(r)
	if err != nil || digest(cookie) != seal.Binding.Session {
		return nil, denied()
	}
	generation, err := s.cfg.Generation(r.Context())
	if err != nil || generation != seal.Binding.Generation {
		return nil, denied()
	}
	err = s.transaction(func(records map[string]record) error {
		rec, ok := records[digest(q.Get(GrantParameter))]
		if !ok || rec.Binding != seal.Binding {
			return denied()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	copy := r.Clone(r.Context())
	copy.Header.Del("SynoToken")
	copy.Header.Set("X-SYNO-TOKEN", seal.Token)
	if _, err = api.DSMSession(copy); err != nil {
		return nil, err
	}
	return copy, nil
}
func (s *Service) unseal(r *http.Request) (sealed, error) {
	cookies := r.CookiesNamed(s.cookie)
	if len(cookies) != 1 || len(cookies[0].Value) > 4096 {
		return sealed{}, denied()
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookies[0].Value)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return sealed{}, denied()
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], []byte(s.cookie))
	if err != nil {
		return sealed{}, denied()
	}
	var seal sealed
	if json.Unmarshal(plain, &seal) != nil || !s.now().Before(seal.Expires) || seal.Binding.Entry != s.cfg.Entry {
		return sealed{}, denied()
	}
	return seal, nil
}
func (s *Service) setSeal(w http.ResponseWriter, r *http.Request, b binding, token string) error {
	secure := r.TLS != nil || r.URL.Scheme == "https" || strings.HasPrefix(r.Header.Get("Origin"), "https://")
	if previous, err := s.unseal(r); err == nil && previous.Binding == b {
		secure = secure || previous.Secure
	}
	raw, err := json.Marshal(sealed{Secure: secure, Binding: b, Token: token, Expires: s.now().Add(s.cfg.TTL)})
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	value := base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, raw, []byte(s.cookie)))
	if len(value) > 3800 {
		return api.BadRequest("DSM session credential is too large for native media")
	}
	if !secure {
		log.Print("media: HTTP is allowed; use HTTPS to protect session cookies")
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: value, Path: s.cfg.Endpoint, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: int(s.cfg.TTL.Seconds()), Expires: s.now().Add(s.cfg.TTL)})
	return nil
}
func (s *Service) grant(id string, rec record) Grant {
	q := url.Values{"method": {s.cfg.Prefix + ".content"}, GrantParameter: {id}}
	return Grant{ID: id, URL: s.cfg.Endpoint + "?" + q.Encode(), ExpiresAt: rec.Expires, TTLMS: s.cfg.TTL.Milliseconds()}
}

func (s *Service) Routes() []api.Route {
	return []api.Route{
		api.HandleResponse(s.cfg.Prefix+".issue", s.issue), api.HandleResponse(s.cfg.Prefix+".renew", s.renew),
		api.HandleRequest(s.cfg.Prefix+".revoke", s.revoke), api.HandleStream(s.cfg.Prefix+".content", s.content),
	}
}
func decode(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return api.BadRequest("invalid media parameters")
	}
	return nil
}

// rpcAdmission keeps issuance/renewal uncached even if ordinary RPCs opt in
// to a session cache. A media cookie can never supply an RPC's token.
func (s *Service) rpcAdmission(c api.Caller, r *http.Request) (map[string]string, error) {
	auth, err := api.DSMSession(r)
	if err != nil {
		return nil, err
	}
	id, err := s.cfg.FreshAuth.Authenticate(r)
	if err != nil || id.User != c.User() {
		return nil, denied()
	}
	return auth, nil
}

func (s *Service) issue(ctx context.Context, c api.Caller, w http.ResponseWriter, r *http.Request, raw json.RawMessage) error {
	auth, err := s.rpcAdmission(c, r)
	if err != nil {
		return err
	}
	var p struct {
		Asset string `json:"asset"`
	}
	if err = decode(raw, &p); err != nil {
		return err
	}
	if p.Asset == "" || len(p.Asset) > 1024 {
		return api.BadRequest("invalid asset")
	}
	b, err := s.currentBinding(ctx, c, r)
	if err != nil {
		return err
	}
	revision, err := s.cfg.Authorize(ctx, c, p.Asset)
	if err != nil {
		return err
	}
	if len(revision) > 1024 {
		return api.BadRequest("invalid owner revision")
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	rec := record{Binding: b, Asset: p.Asset, Revision: revision, Methods: []string{http.MethodGet, http.MethodHead}, Expires: s.now().Add(s.cfg.TTL)}
	err = s.transaction(func(records map[string]record) error {
		if len(records) >= maxGrants {
			return &api.Error{Code: "unavailable", Message: "media grant capacity reached"}
		}
		records[digest(id)] = rec
		return nil
	})
	if err != nil {
		return err
	}
	if err = s.setSeal(w, r, b, auth["dsm_synotoken"]); err != nil {
		_ = s.Revoke(id)
		return err
	}
	privacy(w.Header())
	api.WriteData(w, s.grant(id, rec))
	return nil
}
func (s *Service) checked(ctx context.Context, c api.Caller, r *http.Request, id string) (record, error) {
	if !validID(id) {
		return record{}, denied()
	}
	b, err := s.currentBinding(ctx, c, r)
	if err != nil {
		return record{}, err
	}
	var rec record
	err = s.transaction(func(records map[string]record) error {
		var ok bool
		rec, ok = records[digest(id)]
		if !ok || rec.Binding != b {
			return denied()
		}
		return nil
	})
	if err != nil {
		return record{}, err
	}
	revision, err := s.cfg.Authorize(ctx, c, rec.Asset)
	if err != nil {
		return record{}, err
	}
	if revision != rec.Revision {
		return record{}, denied()
	}
	return rec, nil
}
func (s *Service) extend(id string, rec record) (record, error) {
	rec.Expires = s.now().Add(s.cfg.TTL)
	err := s.transaction(func(records map[string]record) error {
		current, ok := records[digest(id)]
		if !ok || current.Binding != rec.Binding || current.Asset != rec.Asset || current.Revision != rec.Revision {
			return denied()
		}
		records[digest(id)] = rec
		return nil
	})
	return rec, err
}
func (s *Service) renew(ctx context.Context, c api.Caller, w http.ResponseWriter, r *http.Request, raw json.RawMessage) error {
	auth, err := s.rpcAdmission(c, r)
	if err != nil {
		return err
	}
	var p struct {
		ID string `json:"media_grant"`
	}
	if err = decode(raw, &p); err != nil {
		return err
	}
	rec, err := s.checked(ctx, c, r, p.ID)
	if err != nil {
		return err
	}
	rec, err = s.extend(p.ID, rec)
	if err != nil {
		return err
	}
	if err = s.setSeal(w, r, rec.Binding, auth["dsm_synotoken"]); err != nil {
		return err
	}
	privacy(w.Header())
	api.WriteData(w, s.grant(p.ID, rec))
	return nil
}
func (s *Service) revoke(ctx context.Context, c api.Caller, r *http.Request, raw json.RawMessage) (any, error) {
	if _, err := api.DSMSession(r); err != nil {
		return nil, err
	}
	var p struct {
		ID string `json:"media_grant"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	b, err := s.currentBinding(ctx, c, r)
	if err != nil {
		return nil, err
	}
	if !validID(p.ID) {
		return nil, apiBadID()
	}
	err = s.transaction(func(records map[string]record) error {
		if rec, ok := records[digest(p.ID)]; ok && rec.Binding != b {
			return denied()
		}
		delete(records, digest(p.ID))
		return nil
	})
	return map[string]bool{"revoked": err == nil}, err
}
func privacy(h http.Header) {
	h.Set("Cache-Control", "private, no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

type privateResponse struct{ http.ResponseWriter }

func (w privateResponse) WriteHeader(code int) {
	privacy(w.Header())
	w.ResponseWriter.WriteHeader(code)
}
func (w privateResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (s *Service) content(ctx context.Context, c api.Caller, w http.ResponseWriter, r *http.Request) error {
	privacy(w.Header())
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return nil
	}
	id := r.URL.Query().Get(GrantParameter)
	rec, err := s.checked(ctx, c, r, id)
	if err != nil {
		return err
	}
	allowed := false
	for _, method := range rec.Methods {
		allowed = allowed || method == r.Method
	}
	if !allowed {
		return denied()
	}
	asset, err := s.cfg.Open(ctx, c, rec.Asset)
	if err != nil {
		return err
	}
	if asset.Content == nil {
		return errors.New("media opener returned no content")
	}
	defer asset.Content.Close()
	typ, _, err := mime.ParseMediaType(asset.ContentType)
	if err != nil || (!strings.HasPrefix(typ, "audio/") && !strings.HasPrefix(typ, "video/")) || !validETag(asset.ETag) {
		return errors.New("media opener requires audio/video content type and strong ETag")
	}
	if _, err = s.extend(id, rec); err != nil {
		return err
	}
	restored, err := s.restore(r)
	if err != nil {
		return err
	}
	if err = s.setSeal(w, r, rec.Binding, restored.Header.Get("X-SYNO-TOKEN")); err != nil {
		return err
	}
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("ETag", asset.ETag)
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(privateResponse{w}, r, "", time.Time{}, asset.Content)
	return nil
}
func validETag(tag string) bool {
	if len(tag) < 2 || len(tag) > 256 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for _, ch := range tag[1 : len(tag)-1] {
		if ch < 0x21 || ch == '"' || ch == 0x7f {
			return false
		}
	}
	return true
}
