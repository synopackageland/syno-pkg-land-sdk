package idgateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Lifetimes of the provider's credentials. An app keeps its own session after
// sign-in; these only bound the sign-in itself.
const (
	CodeTTL        = time.Minute
	IDTokenTTL     = 5 * time.Minute
	AccessTokenTTL = 5 * time.Minute
	maxPending     = 10000
)

// Roles maps the DSM administrator bit to a claim. Claim "groups" is a JSON
// array holding Admin or User (none when User is empty); any other claim is a
// string. The zero value is groups: ["admin"] for administrators.
type Roles struct {
	Claim string
	Admin string
	User  string
}

// Client is one registered relying party.
type Client struct {
	ID string
	// SecretSHA256 is the SHA-256 of the client secret; the plain secret is
	// only in the app's configuration.
	SecretSHA256 []byte
	// RedirectPaths are exact paths on the origin the browser used for the app.
	RedirectPaths []string
	// NativeRedirects are exact URIs of mobile apps (custom schemes).
	NativeRedirects []string
	RequirePKCE     bool
	Roles           Roles
}

// AuthRequest is a validated authorization request.
type AuthRequest struct {
	ClientID, RedirectURI, State, Nonce, Challenge, Prompt string
	clientIP                                               string
}

// AuthorizeError is a request error the provider reports to the app's
// redirect URI; Code is an OAuth 2.0 error code.
type AuthorizeError struct{ Code string }

func (e *AuthorizeError) Error() string { return "authorize: " + e.Code }

// ErrInvalidRedirect means an unknown client or a redirect URI that is not
// registered for it on this origin. It is never redirected to.
var ErrInvalidRedirect = errors.New("unknown client or redirect URI")

type grant struct {
	AuthRequest
	subject Subject
	expires time.Time
}

type accessToken struct {
	claims   map[string]any
	expires  time.Time
	subject  Subject
	clientIP string
}

// Provider is the embedded OpenID Connect provider. Its HTTP handler serves
// discovery, token, jwks and userinfo on the loopback issuer; the browser
// reaches authorization only through the gateway on the app's origin.
type Provider struct {
	issuer      string
	emailDomain string
	key         *SigningKey
	clients     map[string]Client
	now         func() time.Time
	admit       func(context.Context, Subject, string) error

	mu     sync.Mutex
	codes  map[string]grant
	tokens map[string]accessToken
}

// NewProvider checks that issuer is an http://127.0.0.1:port or
// http://[::1]:port URL: the token endpoint never leaves the NAS.
func NewProvider(issuer, emailDomain string, key *SigningKey, clients []Client, now func() time.Time) (*Provider, error) {
	if !loopbackURL(issuer) {
		return nil, fmt.Errorf("issuer %q must be http://127.0.0.1:<port> or http://[::1]:<port>", issuer)
	}
	p := &Provider{issuer: issuer, emailDomain: emailDomain, key: key, clients: map[string]Client{}, now: now, codes: map[string]grant{}, tokens: map[string]accessToken{}}
	for _, c := range clients {
		if c.ID == "" || len(c.SecretSHA256) != sha256.Size {
			return nil, fmt.Errorf("client %q needs an id and a SHA-256 secret", c.ID)
		}
		p.clients[c.ID] = c
	}
	return p, nil
}

func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Path != "" || u.RawQuery != "" || u.User != nil || u.Port() == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

// The longest state, nonce and prompt an authorization request may carry.
const (
	maxStateBytes  = 2048
	maxPromptBytes = 256
)

// Begin validates an authorization request that reached the gateway on
// origin, the scheme and host the browser used for the app.
func (p *Provider) Begin(origin string, q url.Values) (AuthRequest, error) {
	c, ok := p.clients[q.Get("client_id")]
	req := AuthRequest{ClientID: c.ID, RedirectURI: q.Get("redirect_uri"), State: q.Get("state"), Nonce: q.Get("nonce"), Challenge: q.Get("code_challenge"), Prompt: q.Get("prompt")}
	if !ok || !allowedRedirect(c, origin, req.RedirectURI) {
		return AuthRequest{}, ErrInvalidRedirect
	}
	method := q.Get("code_challenge_method")
	switch {
	case q.Get("response_type") != "code":
		return req, &AuthorizeError{"unsupported_response_type"}
	case !slices.Contains(strings.Fields(q.Get("scope")), "openid"):
		return req, &AuthorizeError{"invalid_scope"}
	case req.Challenge != "" && method != "S256", req.Challenge == "" && c.RequirePKCE, len(req.Challenge) > 128:
		return req, &AuthorizeError{"invalid_request"}
	case len(req.State) > maxStateBytes, len(req.Nonce) > maxStateBytes, len(req.Prompt) > maxPromptBytes:
		// A pending sign-in keeps these until it completes or expires.
		return req, &AuthorizeError{"invalid_request"}
	}
	return req, nil
}

func allowedRedirect(c Client, origin, raw string) bool {
	if slices.Contains(c.NativeRedirects, raw) {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.Fragment == "" && u.User == nil && u.Scheme+"://"+u.Host == origin && slices.Contains(c.RedirectPaths, u.Path)
}

// Complete issues a one-time code for subject and returns the redirect URI.
func (p *Provider) Complete(req AuthRequest, subject Subject) string {
	code := randomID(32)
	p.mu.Lock()
	p.sweep()
	p.codes[code] = grant{AuthRequest: req, subject: subject, expires: p.now().Add(CodeTTL)}
	p.mu.Unlock()
	return withParams(req.RedirectURI, url.Values{"code": {code}, "state": {req.State}})
}

// Fail returns the redirect URI carrying an OAuth error.
func (p *Provider) Fail(req AuthRequest, code string) string {
	return withParams(req.RedirectURI, url.Values{"error": {code}, "state": {req.State}})
}

func withParams(raw string, add url.Values) string {
	u, _ := url.Parse(raw)
	q := u.Query()
	for k, v := range add {
		if v[0] != "" {
			q[k] = v
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// sweep drops expired entries and, past maxPending, arbitrary live ones until
// the caller's new entry fits; callers hold mu.
func (p *Provider) sweep() {
	now := p.now()
	for k, g := range p.codes {
		if now.After(g.expires) || len(p.codes) >= maxPending {
			delete(p.codes, k)
		}
	}
	for k, t := range p.tokens {
		if now.After(t.expires) || len(p.tokens) >= maxPending {
			delete(p.tokens, k)
		}
	}
}

// RevokeUID drops the codes and access tokens issued for the account uid.
func (p *Provider) RevokeUID(uid int) {
	sub := strconv.Itoa(uid)
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, g := range p.codes {
		if g.subject.UID == uid {
			delete(p.codes, k)
		}
	}
	for k, t := range p.tokens {
		if t.claims["sub"] == sub {
			delete(p.tokens, k)
		}
	}
}

func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeJSON(w, http.StatusOK, p.discovery())
	case "/jwks":
		w.Header().Set("Content-Type", "application/json")
		w.Write(p.key.JWKS())
	case "/token":
		p.token(w, r)
	case "/userinfo":
		p.userinfo(w, r)
	case "/authorize":
		http.Error(w, "sign in through the app's address", http.StatusBadRequest)
	default:
		http.NotFound(w, r)
	}
}

func (p *Provider) discovery() map[string]any {
	return map[string]any{
		"issuer":                                p.issuer,
		"authorization_endpoint":                p.issuer + "/authorize",
		"token_endpoint":                        p.issuer + "/token",
		"jwks_uri":                              p.issuer + "/jwks",
		"userinfo_endpoint":                     p.issuer + "/userinfo",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email", "groups"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":      []string{"S256"},
		"claims_supported":                      []string{"sub", "name", "preferred_username", "email", "email_verified", "groups"},
	}
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "invalid_request"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	client, ok := p.authenticateClient(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="token"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	// A code is consumed by its first presentation, whatever the outcome.
	p.mu.Lock()
	g, found := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	p.mu.Unlock()
	if !found || p.now().After(g.expires) || g.ClientID != client.ID || g.RedirectURI != r.PostForm.Get("redirect_uri") || !pkceMatches(g.Challenge, r.PostForm.Get("code_verifier")) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	if p.admit != nil && p.admit(r.Context(), g.subject, g.AuthRequest.clientIP) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	now := p.now()
	claims := p.claims(client.ID, g.subject)
	idClaims := map[string]any{"iss": p.issuer, "aud": client.ID, "iat": now.Unix(), "exp": now.Add(IDTokenTTL).Unix(), "auth_time": now.Unix()}
	for k, v := range claims {
		idClaims[k] = v
	}
	if g.Nonce != "" {
		idClaims["nonce"] = g.Nonce
	}
	idToken, err := p.key.Sign(idClaims)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	access := randomID(32)
	p.mu.Lock()
	p.sweep()
	p.tokens[access] = accessToken{claims: claims, expires: now.Add(AccessTokenTTL), subject: g.subject, clientIP: g.AuthRequest.clientIP}
	p.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": int(AccessTokenTTL.Seconds()), "id_token": idToken, "scope": "openid profile email groups"})
}

func (p *Provider) authenticateClient(r *http.Request) (Client, bool) {
	id, secret, basic := r.BasicAuth()
	if basic {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	c, ok := p.clients[id]
	sum := sha256.Sum256([]byte(secret))
	return c, ok && secret != "" && subtle.ConstantTimeCompare(sum[:], c.SecretSHA256) == 1
}

func pkceMatches(challenge, verifier string) bool {
	if challenge == "" {
		return verifier == ""
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier != "" && subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	p.mu.Lock()
	t, found := p.tokens[bearer]
	p.mu.Unlock()
	if !ok || !found || p.now().After(t.expires) || p.admit != nil && p.admit(r.Context(), t.subject, t.clientIP) != nil {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	writeJSON(w, http.StatusOK, t.claims)
}

var emailLocal = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// emailAddress is the generated address of s: the lower-cased name when it is
// a plain local part, otherwise uid-<uid>.
func emailAddress(s Subject, domain string) string {
	local := strings.ToLower(s.Name)
	if !emailLocal.MatchString(local) {
		local = "uid-" + strconv.Itoa(s.UID)
	}
	return local + "@" + domain
}

func (r Roles) orDefault() Roles {
	if r.Claim == "" && r.Admin == "" && r.User == "" {
		return Roles{Claim: "groups", Admin: "admin"}
	}
	if r.Claim == "" {
		r.Claim = "groups"
	}
	return r
}

func (r Roles) value(admin bool) string {
	if admin {
		return r.Admin
	}
	return r.User
}

// claims are the identity claims for client: sub is the DSM uid, the email is
// generated from the name (or uid) so no app trusts a user-entered address.
func (p *Provider) claims(clientID string, s Subject) map[string]any {
	c := map[string]any{"sub": strconv.Itoa(s.UID), "name": s.Name, "preferred_username": s.Name, "email": emailAddress(s, p.emailDomain), "email_verified": true}
	roles := p.clients[clientID].Roles.orDefault()
	role := roles.value(s.Admin)
	switch {
	case roles.Claim != "groups":
		c[roles.Claim] = role
	case role == "":
		c["groups"] = []string{}
	default:
		c["groups"] = []string{role}
	}
	return c
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
