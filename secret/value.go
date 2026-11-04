// Package secret provides request-only credentials and safe diagnostic values.
package secret

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
)

const Redacted = "[redacted]"

// Value holds an external-service credential for one request. Ordinary
// formatting and serialization never expose it. Reveal is only for the
// immediate external connection, never for settings, jobs or diagnostics.
// Copies share the same bytes, so Clear invalidates every copy.
type Value struct{ data *value }
type value struct{ bytes []byte }

func New(raw string) Value { return Value{data: &value{bytes: []byte(raw)}} }
func (v Value) Reveal() string {
	if v.data == nil {
		return ""
	}
	return string(v.data.bytes)
}
func (v Value) Clear() {
	if v.data != nil {
		clear(v.data.bytes)
		v.data.bytes = nil
	}
}
func (v Value) String() string               { return Redacted }
func (v Value) GoString() string             { return Redacted }
func (v Value) Format(s fmt.State, _ rune)   { _, _ = io.WriteString(s, Redacted) }
func (v Value) LogValue() slog.Value         { return slog.StringValue(Redacted) }
func (v Value) MarshalJSON() ([]byte, error) { return json.Marshal(Redacted) }
func (v Value) MarshalText() ([]byte, error) { return []byte(Redacted), nil }
func (v Value) GobEncode() ([]byte, error)   { return []byte(Redacted), nil }
func (v *Value) GobDecode([]byte) error      { return errors.New("request-only secret cannot be restored") }
func (v *Value) UnmarshalJSON(raw []byte) error {
	v.Clear()
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return errors.New("secret must be a string")
	}
	if *value == Redacted {
		return errors.New("request-only secret cannot be restored")
	}
	*v = New(*value)
	return nil
}
