package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"time"
	"unicode/utf8"
)

// Bounds a policy may declare for its root programs.
const (
	defaultRootRunTimeout = 20 * time.Second
	maxRootRunTimeout     = 120 * time.Second
	defaultRootOutput     = 1 << 20
	maxRootOutput         = 64 << 20
	defaultRootJobTimeout = 24 * time.Hour
	maxRootJobTimeout     = 7 * 24 * time.Hour
	defaultRootJobGrace   = 10 * time.Second
	maxRootJobGrace       = time.Minute
	maxRootJobConcurrency = 4
	defaultRootJobNice    = 10
)

// RootEntry is one root program the approved policy pins. A bare digest
// string is a script that root.run may run with the defaults.
type RootEntry struct {
	Digest string `json:"digest"`
	// Kind is script (root/<name>.sh, run by /bin/sh) or binary (root/<name>).
	Kind string `json:"kind,omitempty"`
	// Schema is the JSON Schema subset the executor checks params against;
	// without one the program takes no params.
	Schema *paramSchema `json:"schema,omitempty"`
	// Run lets root.run run the program, Job lets root.start start it. An
	// entry with neither is only a lifecycle step.
	Run *RootRun `json:"run,omitempty"`
	Job *RootJob `json:"job,omitempty"`
	// shorthand marks the bare digest form, which a stop or purge step may
	// use without becoming a session program.
	shorthand bool
}

// A member left out takes its default; a declared one must be in bounds.
type RootRun struct {
	TimeoutS  *int   `json:"timeout_s,omitempty"`
	MaxOutput *int64 `json:"max_output,omitempty"`
}

type RootJob struct {
	TimeoutS      *int `json:"timeout_s,omitempty"`
	GraceS        *int `json:"grace_s,omitempty"`
	MaxConcurrent *int `json:"max_concurrent,omitempty"`
	Nice          *int `json:"nice,omitempty"`
}

func inBounds[T int | int64](v *T, low, high T) bool { return v == nil || (*v >= low && *v <= high) }

func value[T int | int64](v *T, fallback T) T {
	if v == nil {
		return fallback
	}
	return *v
}

func (r *RootEntry) UnmarshalJSON(b []byte) error {
	var digest string
	if json.Unmarshal(b, &digest) == nil {
		*r = RootEntry{Digest: digest, Kind: "script", Run: &RootRun{}, shorthand: true}
		return nil
	}
	type plain RootEntry
	var entry plain
	if err := strictJSON(b, &entry); err != nil {
		return err
	}
	*r = RootEntry(entry)
	if r.Kind == "" {
		r.Kind = "script"
	}
	return nil
}

func (r RootEntry) valid() bool {
	if !idRE.MatchString(r.Digest) || (r.Kind != "script" && r.Kind != "binary") {
		return false
	}
	if r.Schema != nil && (r.Schema.Type != "object" || r.Schema.validate(0) != nil) {
		return false
	}
	if r.Run != nil && (!inBounds(r.Run.TimeoutS, 1, int(maxRootRunTimeout/time.Second)) || !inBounds(r.Run.MaxOutput, 1, maxRootOutput)) {
		return false
	}
	j := r.Job
	return j == nil || (inBounds(j.TimeoutS, 1, int(maxRootJobTimeout/time.Second)) && inBounds(j.GraceS, 1, int(maxRootJobGrace/time.Second)) &&
		inBounds(j.MaxConcurrent, 1, maxRootJobConcurrency) && inBounds(j.Nice, 0, 19))
}

// session reports whether a session may run or start the program: the
// policy declares it with run or job rather than as the shorthand digest.
func (r RootEntry) session() bool { return !r.shorthand && (r.Run != nil || r.Job != nil) }

func (r *RootRun) timeout() time.Duration {
	return time.Duration(value(r.TimeoutS, int(defaultRootRunTimeout/time.Second))) * time.Second
}
func (r *RootRun) maxOutput() int64 { return value(r.MaxOutput, defaultRootOutput) }
func (j *RootJob) timeout() time.Duration {
	return time.Duration(value(j.TimeoutS, int(defaultRootJobTimeout/time.Second))) * time.Second
}
func (j *RootJob) grace() time.Duration {
	return time.Duration(value(j.GraceS, int(defaultRootJobGrace/time.Second))) * time.Second
}
func (j *RootJob) concurrency() int { return value(j.MaxConcurrent, 1) }
func (j *RootJob) nice() int        { return value(j.Nice, defaultRootJobNice) }

// paramSchema is the JSON Schema subset a policy may declare for root
// params: a closed object of strings, integers, numbers, booleans, arrays
// and nested closed objects. Unknown keywords make the policy invalid.
type paramSchema struct {
	Type                 string                  `json:"type"`
	Enum                 []json.RawMessage       `json:"enum,omitempty"`
	Pattern              string                  `json:"pattern,omitempty"`
	MinLength            *int                    `json:"minLength,omitempty"`
	MaxLength            *int                    `json:"maxLength,omitempty"`
	Minimum              *float64                `json:"minimum,omitempty"`
	Maximum              *float64                `json:"maximum,omitempty"`
	Items                *paramSchema            `json:"items,omitempty"`
	MinItems             *int                    `json:"minItems,omitempty"`
	MaxItems             *int                    `json:"maxItems,omitempty"`
	Properties           map[string]*paramSchema `json:"properties,omitempty"`
	Required             []string                `json:"required,omitempty"`
	AdditionalProperties *bool                   `json:"additionalProperties,omitempty"`
	re                   *regexp.Regexp
}

var paramKeyRE = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// validate checks the schema itself and compiles its patterns.
func (s *paramSchema) validate(depth int) error {
	if s == nil || depth > 8 {
		return errors.New("schema depth")
	}
	stringy := s.Pattern != "" || s.MinLength != nil || s.MaxLength != nil
	numeric := s.Minimum != nil || s.Maximum != nil
	array := s.Items != nil || s.MinItems != nil || s.MaxItems != nil
	object := s.Properties != nil || s.Required != nil || s.AdditionalProperties != nil
	switch s.Type {
	case "string":
		if numeric || array || object {
			return errors.New("schema keyword")
		}
		if s.Pattern != "" {
			re, err := regexp.Compile(s.Pattern)
			if err != nil {
				return err
			}
			s.re = re
		}
	case "integer", "number":
		if stringy || array || object {
			return errors.New("schema keyword")
		}
	case "boolean":
		if stringy || numeric || array || object {
			return errors.New("schema keyword")
		}
	case "array":
		if stringy || numeric || object || s.Items == nil {
			return errors.New("schema keyword")
		}
		if err := s.Items.validate(depth + 1); err != nil {
			return err
		}
	case "object":
		if stringy || numeric || array || s.AdditionalProperties == nil || *s.AdditionalProperties || len(s.Enum) > 0 {
			return errors.New("schema object must be closed")
		}
		for key, p := range s.Properties {
			if !paramKeyRE.MatchString(key) {
				return errors.New("schema key")
			}
			if err := p.validate(depth + 1); err != nil {
				return err
			}
		}
		for _, key := range s.Required {
			if s.Properties[key] == nil {
				return errors.New("schema required")
			}
		}
	default:
		return errors.New("schema type")
	}
	for _, v := range s.Enum {
		value, err := decodeNumbers(v)
		if err != nil || s.match(value, 0) != nil {
			return errors.New("schema enum")
		}
	}
	return nil
}

// check validates params, a JSON object, against the schema; a nil schema
// accepts only an empty object.
func (s *paramSchema) check(params []byte) error {
	value, err := decodeNumbers(params)
	if err != nil {
		return err
	}
	if s == nil {
		if m, ok := value.(map[string]any); ok && len(m) == 0 {
			return nil
		}
		return errors.New("params without a schema")
	}
	return s.match(value, 0)
}

func (s *paramSchema) match(value any, depth int) error {
	if depth > 16 {
		return errors.New("params depth")
	}
	if len(s.Enum) > 0 {
		got, err := json.Marshal(value)
		if err != nil {
			return err
		}
		found := false
		for _, want := range s.Enum {
			w, _ := decodeNumbers(want)
			canonical, _ := json.Marshal(w)
			found = found || bytes.Equal(canonical, got)
		}
		if !found {
			return errors.New("params enum")
		}
	}
	switch s.Type {
	case "string":
		v, ok := value.(string)
		n := utf8.RuneCountInString(v)
		if !ok || (s.MinLength != nil && n < *s.MinLength) || (s.MaxLength != nil && n > *s.MaxLength) || (s.re != nil && !s.re.MatchString(v)) {
			return errors.New("params string")
		}
	case "integer", "number":
		v, ok := value.(json.Number)
		if !ok {
			return errors.New("params number")
		}
		f, err := v.Float64()
		if err != nil {
			return errors.New("params number")
		}
		if s.Type == "integer" {
			if _, err := v.Int64(); err != nil {
				return errors.New("params integer")
			}
		}
		if (s.Minimum != nil && f < *s.Minimum) || (s.Maximum != nil && f > *s.Maximum) {
			return errors.New("params range")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return errors.New("params boolean")
		}
	case "array":
		v, ok := value.([]any)
		if !ok || (s.MinItems != nil && len(v) < *s.MinItems) || (s.MaxItems != nil && len(v) > *s.MaxItems) {
			return errors.New("params array")
		}
		for _, item := range v {
			if err := s.Items.match(item, depth+1); err != nil {
				return err
			}
		}
	case "object":
		v, ok := value.(map[string]any)
		if !ok {
			return errors.New("params object")
		}
		for key, item := range v {
			p := s.Properties[key]
			if p == nil {
				return errors.New("params member")
			}
			if err := p.match(item, depth+1); err != nil {
				return err
			}
		}
		for _, key := range s.Required {
			if _, ok := v[key]; !ok {
				return errors.New("params required")
			}
		}
	}
	return nil
}

// decodeNumbers decodes JSON keeping numbers as written, so 1.0 is no integer.
func decodeNumbers(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value any
	err := d.Decode(&value)
	return value, err
}
