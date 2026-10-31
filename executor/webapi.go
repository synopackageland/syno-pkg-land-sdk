package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/budget"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxWebAPIParams = 32 << 10
const maxWebAPIReply = 48 << 10
const webAPITimeout = 10 * time.Second

var webAPIName = regexp.MustCompile(`^SYNO\.[A-Za-z][A-Za-z0-9]*(\.[A-Za-z][A-Za-z0-9]*)*$`)
var webAPIMethod = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var webAPIParam = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func validWebAPINames(api, method string) bool {
	return webAPIName.MatchString(api) && webAPIMethod.MatchString(method)
}
func profileDigest(p WebAPIProfile) string { b, _ := json.Marshal(p); return hashBytes(b) }
func (p WebAPIProfile) allows(api, method string, version int) bool {
	if !validWebAPINames(api, method) {
		return false
	}
	for _, v := range p.APIs[api][method] {
		if v == version {
			return true
		}
	}
	return false
}
func validWebAPIProfiles(p Policy) bool {
	if len(p.WebAPI) > 32 || (len(p.WebAPI) > 0) != contains(p.Operations, "webapi.call") {
		return false
	}
	if len(p.WebAPI) > 0 && !contains(p.Operations, "delegate.create") {
		return false
	}
	total := 0
	for name, profile := range p.WebAPI {
		if profile.RawMaxBytes < 0 || profile.RawMaxBytes > maxWebAPIRaw {
			return false
		}
		if !destinationRE.MatchString(name) || name == "home" || strings.TrimSpace(profile.Purpose) == "" || len(profile.Purpose) > 1024 || strings.ContainsFunc(profile.Purpose, unicode.IsControl) || len(profile.APIs) == 0 {
			return false
		}
		for api, methods := range profile.APIs {
			if len(methods) == 0 {
				return false
			}
			for method, versions := range methods {
				if !validWebAPINames(api, method) || len(versions) == 0 {
					return false
				}
				seen := map[int]bool{}
				for _, v := range versions {
					total++
					if v < 1 || v > 65535 || seen[v] || total > 256 {
						return false
					}
					seen[v] = true
				}
			}
		}
	}
	return true
}
func isWebGrant(d Delegation) bool {
	return len(d.Ops) == 1 && d.Ops[0] == "webapi.call" && d.WebAPIProfileDigest != ""
}
func (e *Executor) webGrantMatches(d Delegation) bool {
	p, ok := e.Policy.WebAPI[d.Scope]
	return isWebGrant(d) && ok && d.WebAPIProfileDigest == profileDigest(p) && contains(e.Policy.Operations, "webapi.call")
}

func (e *Executor) createWebAPI(ctx context.Context, auth Auth, p Params, keys map[string]json.RawMessage) Response {
	long := p.Lifetime == "until-revoked"
	if len(p.Ops) != 1 || p.Ops[0] != "webapi.call" || p.Path != "" || p.DestinationKey != "" ||
		(long && (p.Job != "" || p.TTL != 0)) || (!long && (p.Lifetime != "" || !tokenRE.MatchString(p.Job) || p.TTL < 1 || p.TTL > 86400*30)) {
		return deny("INVALID_REQUEST")
	}
	for _, k := range []string{"path", "destination"} {
		if _, ok := keys[k]; ok {
			return deny("INVALID_REQUEST")
		}
	}
	if long {
		for _, k := range []string{"job_id", "ttl_seconds"} {
			if _, ok := keys[k]; ok {
				return deny("INVALID_REQUEST")
			}
		}
	}
	profile, ok := e.Policy.WebAPI[p.Scope]
	if !ok || !contains(e.Policy.Operations, "webapi.call") {
		return deny("POLICY_DENIED")
	}
	s, code := e.verified(ctx, auth)
	if code != "" {
		return deny(code)
	}
	s, code = e.privileged(ctx, s, s.Username, s.UID, false)
	if code != "" {
		return deny(code)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return deny("INTERNAL")
	}
	d := Delegation{ID: hex.EncodeToString(id), Package: e.Policy.Package, Subject: s, Ops: p.Ops, Scope: p.Scope, Job: p.Job, Lifetime: p.Lifetime, WebAPIProfileDigest: profileDigest(profile)}
	d.ClientAddr, _ = clientAddress(ctx)
	if !long {
		d.Expires = e.Now().UTC().Add(time.Duration(p.TTL) * time.Second)
	}
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	disabled, err := e.Store.Disabled()
	if err != nil {
		return deny("STORAGE_ERROR")
	}
	if disabled {
		return deny("disabled")
	}
	// A retry of an until-revoked consent keeps the active profile grant.
	if long {
		grants, err := e.Store.List()
		if err != nil {
			return deny("STORAGE_ERROR")
		}
		for _, prior := range grants {
			if prior.Package == d.Package && sameIdentity(prior.Subject, s) && prior.Scope == d.Scope && prior.Lifetime == d.Lifetime && !prior.Revoked && e.webGrantMatches(prior) {
				prior.ClientAddr = d.ClientAddr
				if e.Store.Put(prior) != nil {
					return deny("STORAGE_ERROR")
				}
				return success(publicDelegation(prior))
			}
		}
	}
	if e.auditLocked(s, "delegate.create", d.ID, d.Scope, "admitting") != nil || e.Store.Put(d) != nil {
		return deny("STORAGE_ERROR")
	}
	return success(publicDelegation(d))
}

type webAPIRequest struct {
	ID           string          `json:"delegation_id"`
	Job          string          `json:"job_id,omitempty"`
	API          string          `json:"api"`
	Method       string          `json:"method"`
	Version      int             `json:"version"`
	Params       json.RawMessage `json:"params"`
	ResponseType string          `json:"response_type,omitempty"`
	ExpectedSize *int64          `json:"expected_size,omitempty"`
}

func rawWebAPIRequest(raw json.RawMessage) bool {
	var p webAPIRequest
	return json.Unmarshal(raw, &p) == nil && p.ResponseType == "raw"
}

// Preserve canonical wire/auth keys while package API payloads may use mixed
// case property names. Duplicate keys at every depth are still refused.
func strictWebJSON(raw []byte, out any, allowed string) error {
	if !utf8.Valid(raw) || !json.Valid(raw) || uniqueValue(json.NewDecoder(bytes.NewReader(raw)), false, false) != nil {
		return errors.New("WebAPI JSON")
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil || keys == nil {
		return errors.New("WebAPI object")
	}
	for key := range keys {
		if !contains(strings.Fields(allowed), key) {
			return errors.New("WebAPI key")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(out) != nil || dec.Decode(new(any)) != io.EOF {
		return errors.New("WebAPI decode")
	}
	return nil
}
func webAPIParams(raw json.RawMessage) (map[string]any, string) {
	if len(raw) > maxWebAPIParams {
		return nil, "REQUEST_TOO_LARGE"
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, "INVALID_REQUEST"
	}
	var params map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&params) != nil || params == nil {
		return nil, "INVALID_REQUEST"
	}
	for k := range params {
		lower := strings.ToLower(k)
		if !webAPIParam.MatchString(k) || strings.HasPrefix(k, "_") || contains([]string{"api", "method", "version", "runner", "outfile", "sid", "cookie", "synotoken", "synoconfirmpwtoken", "passwd", "password", "account", "session", "user", "username", "uid", "auth", "authorization", "token", "access_token", "refresh_token", "auth_token", "remote_addr", "compound", "stopwhenerror"}, lower) {
			return nil, "INVALID_REQUEST"
		}
	}
	return params, ""
}

type webAPICaller interface {
	WebAPI(context.Context, string, string, string, int, map[string]any) (json.RawMessage, error)
}

func (e *Executor) callWebAPI(ctx context.Context, r Request) Response {
	var p webAPIRequest
	if strictJSON(r.Params, &p) != nil || !idRE.MatchString(p.ID) || (p.ResponseType != "" && p.ResponseType != "json" && p.ResponseType != "raw") {
		return deny("INVALID_REQUEST")
	}
	params, code := webAPIParams(p.Params)
	if code != "" {
		return deny(code)
	}
	e.commitMu.Lock()
	d, err := e.Store.Get(p.ID)
	e.commitMu.Unlock()
	if err != nil || d.Package != e.Policy.Package || d.Revoked || !e.webGrantMatches(d) || d.Job != p.Job || (d.Lifetime != "until-revoked" && !e.Now().Before(d.Expires)) {
		return deny("DELEGATION_DENIED")
	}
	if !e.Policy.WebAPI[d.Scope].allows(p.API, p.Method, p.Version) {
		return deny("POLICY_DENIED")
	}
	if p.ResponseType == "raw" && e.Policy.WebAPI[d.Scope].RawMaxBytes <= 0 {
		return deny("POLICY_DENIED")
	}
	if p.ExpectedSize != nil && (p.ResponseType != "raw" || *p.ExpectedSize < 0 || *p.ExpectedSize > e.Policy.WebAPI[d.Scope].RawMaxBytes) {
		return deny("INVALID_REQUEST")
	}
	if r.Auth.present() {
		caller, code := e.verified(ctx, r.Auth)
		if code != "" {
			return deny(code)
		}
		if caller.Username != d.Subject.Username || caller.UID != d.Subject.UID {
			return deny("DELEGATION_DENIED")
		}
	}
	ctx = withGrantAddress(ctx, r.Auth, d.ClientAddr)
	timeout := webAPITimeout
	if p.ResponseType == "raw" {
		timeout = webAPIRawTimeout
		if p.ExpectedSize != nil {
			timeout = budget.WebAPIRawForSize(*p.ExpectedSize)
		}
		if d.Lifetime != "until-revoked" {
			timeout = min(timeout, d.Expires.Sub(e.Now()))
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s, code := e.eligible(ctx, d.Subject.Username, d.Subject.UID, false)
	if code != "" {
		return deny(code)
	}
	done, code := e.beginLegacy(d)
	if code != "" {
		return deny(code)
	}
	defer done()
	if p.ResponseType == "raw" {
		return e.callWebAPIRaw(ctx, s, d, p, params)
	}
	caller, ok := e.DSM.(webAPICaller)
	if !ok {
		return deny("WEBAPI_UNCERTAIN")
	}
	target := d.Scope + ":" + p.API + "/" + p.Method + "/" + strconv.Itoa(p.Version)
	e.commitMu.Lock()
	err = e.auditLocked(s, "webapi.call", d.ID, target, "admitting")
	e.commitMu.Unlock()
	if err != nil {
		return deny("STORAGE_ERROR")
	}
	out, err := caller.WebAPI(ctx, s.Username, p.API, p.Method, p.Version, params)
	result := "completed"
	if err != nil || len(out) > maxWebAPIReply || !json.Valid(out) {
		result = "uncertain"
	}
	e.commitMu.Lock()
	auditErr := e.auditLocked(s, "webapi.call", d.ID, target, result)
	e.commitMu.Unlock()
	// Once dispatched, any missing reply is ambiguous: no automatic retries.
	if result == "uncertain" || auditErr != nil {
		return deny("WEBAPI_UNCERTAIN")
	}
	return success(out)
}

// WebAPI is deliberately separate from files.*' fixed userCalls map. The
// executor admits the approved profile first; this adapter repeats the name
// format and reserved-parameter checks before constructing fixed argv.
func (d *liveDSM) WebAPI(ctx context.Context, runner, api, method string, version int, params map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if runner == "" || !validWebAPINames(api, method) || version < 1 || version > 65535 {
		return nil, errors.New("WebAPI denied")
	}
	if _, code := webAPIParams(raw); code != "" {
		return nil, errors.New("WebAPI params")
	}
	out, err := d.exec(ctx, api, method, strconv.Itoa(version), runner, params)
	if err != nil {
		return nil, err
	}
	if len(out) > maxWebAPIReply {
		return nil, errors.New("WebAPI reply size")
	}
	var envelope struct {
		Success *bool `json:"success"`
	}
	if strictJSON(out, &map[string]json.RawMessage{}) != nil || json.Unmarshal(out, &envelope) != nil || envelope.Success == nil {
		return nil, errors.New("WebAPI envelope")
	}
	return json.RawMessage(out), nil
}
