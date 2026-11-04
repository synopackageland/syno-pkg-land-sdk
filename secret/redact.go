package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
)

// SensitiveKey recognizes credential names across JSON, headers and form data.
func SensitiveKey(key string) bool {
	for _, name := range []string{"password", "passwd", "passphrase", "otp", "token", "secret", "authorization", "cookie", "synotoken", "sid", "X-SYNO-TOKEN"} {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// Redact returns a detached JSON-safe diagnostic copy. Unsupported values fail
// closed; the original value and request bodies must never be logged instead.
func Redact(value any) any { return (&Redactor{}).Redact(value) }

// Redactor is local to one request. It also removes declared credential values
// from unlabelled messages and other fields in diagnostic copies.
type Redactor struct {
	values []string
	fields []string
}

func NewRedactor(raw json.RawMessage, fields ...string) *Redactor {
	r := &Redactor{fields: append([]string(nil), fields...)}
	var value any
	if decode(raw, &value) == nil {
		r.collect(value, false)
	}
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
	return r
}

// IncludeRequestCredentials keeps session/header credentials out of the same
// request's diagnostics. It retains no request pointer and never reads a body.
func (r *Redactor) IncludeRequestCredentials(req *http.Request) {
	if req == nil {
		return
	}
	for key, values := range req.Header {
		if !SensitiveKey(key) {
			continue
		}
		for _, value := range values {
			if value != "" {
				r.values = append(r.values, value)
			}
			if strings.EqualFold(key, "Authorization") {
				if _, credential, ok := strings.Cut(value, " "); ok && credential != "" {
					r.values = append(r.values, credential)
				}
			}
		}
	}
	for _, cookie := range req.Cookies() {
		if cookie.Value != "" {
			r.values = append(r.values, cookie.Value)
		}
	}
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
}

func (r *Redactor) sensitive(key string) bool {
	if SensitiveKey(key) {
		return true
	}
	for _, field := range r.fields {
		if field == key {
			return true
		}
	}
	return false
}
func (r *Redactor) collect(value any, sensitive bool) {
	switch v := value.(type) {
	case string:
		if sensitive && v != "" && v != Redacted {
			r.values = append(r.values, v)
		}
	case json.Number:
		if sensitive {
			r.values = append(r.values, v.String())
		}
	case map[string]any:
		for key, item := range v {
			r.collect(item, sensitive || r.sensitive(key))
		}
	case []any:
		for _, item := range v {
			r.collect(item, sensitive)
		}
	}
}
func (r *Redactor) Text(text string) string {
	// Replace longest first so overlapping credentials cannot reveal a suffix.
	for _, value := range r.values {
		text = strings.ReplaceAll(text, value, Redacted)
	}
	return text
}
func (r *Redactor) Redact(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return Redacted
	}
	var decoded any
	if decode(raw, &decoded) != nil {
		return Redacted
	}
	return r.walk(decoded)
}
func (r *Redactor) walk(value any) any {
	switch v := value.(type) {
	case string:
		return r.Text(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if r.sensitive(key) {
				out[r.Text(key)] = Redacted
			} else {
				out[r.Text(key)] = r.walk(item)
			}
		}
		return out
	case []any:
		for i, item := range v {
			v[i] = r.walk(item)
		}
	}
	return value
}

type scopeKey struct{}

var ErrPersistence = errors.New("request-only credentials cannot enter a persistent store")

// WithScope marks credential-bearing work as request-only. Durable SDK jobs
// refuse this scope; it must not be detached to enqueue credentials for replay.
func (r *Redactor) WithScope(ctx context.Context) context.Context {
	active, _ := ctx.Value(scopeKey{}).(bool)
	return context.WithValue(ctx, scopeKey{}, active || len(r.values) > 0)
}
func RefusePersistence(ctx context.Context) error {
	if active, _ := ctx.Value(scopeKey{}).(bool); active {
		return ErrPersistence
	}
	return nil
}

// RequestMetadata removes bodies, query strings and credential header
// values before passing request metadata to an SDK diagnostic observer.
func RequestMetadata(r *http.Request) *http.Request {
	safe := r.Clone(r.Context())
	safe.Body = http.NoBody
	safe.GetBody = nil
	safe.ContentLength = 0
	safe.Form = nil
	safe.PostForm = nil
	safe.MultipartForm = nil
	safe.URL.User = nil
	safe.URL.RawQuery = ""
	safe.URL.ForceQuery = false
	safe.URL.Fragment = ""
	safe.RequestURI = safe.URL.EscapedPath()
	for key := range safe.Header {
		if SensitiveKey(key) {
			safe.Header.Set(key, Redacted)
		}
	}
	for key := range safe.Trailer {
		if SensitiveKey(key) {
			safe.Trailer.Set(key, Redacted)
		}
	}
	return safe
}

func decode(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(value)
}
