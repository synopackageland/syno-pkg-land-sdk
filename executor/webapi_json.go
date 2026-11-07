package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// webJSONOutput discards only insignificant JSON whitespace as stdout arrives.
// It retains one compact payload, never the pretty output or a decoded tree.
// Small replies grow through modest buckets before the fixed ceiling.
type webJSONOutput struct {
	buf               []byte
	inString, escaped bool
	gap               bool
}

type webJSONSlotKey struct{}

type webAPIReplyTooLarge struct{ Size int }

func (e *webAPIReplyTooLarge) Error() string {
	return fmt.Sprintf("WebAPI compact reply size is at least %d bytes; limit is %d bytes", e.Size, maxWebAPIReply)
}

func (w *webJSONOutput) Write(p []byte) (int, error) {
	for i, c := range p {
		if !w.inString && (c == ' ' || c == '\t' || c == '\r' || c == '\n') {
			w.gap = true
			continue
		}
		if w.gap && len(w.buf) > 0 && !bytes.ContainsRune([]byte("{}[],:\""), rune(w.buf[len(w.buf)-1])) && !bytes.ContainsRune([]byte("{}[],:\""), rune(c)) {
			return i, errors.New("whitespace inside JSON token")
		}
		w.gap = false
		if len(w.buf) == maxWebAPIReply {
			return i, &webAPIReplyTooLarge{maxWebAPIReply + 1}
		}
		if len(w.buf) == cap(w.buf) {
			capacity := 32 << 10
			switch cap(w.buf) {
			case 32 << 10:
				capacity = 256 << 10
			case 256 << 10:
				capacity = 1 << 20
			case 1 << 20:
				capacity = maxWebAPIReply
			}
			next := make([]byte, len(w.buf), capacity)
			copy(next, w.buf)
			w.buf = next
		}
		w.buf = append(w.buf, c)
		if w.escaped {
			w.escaped = false
		} else if w.inString && c == '\\' {
			w.escaped = true
		} else if c == '"' {
			w.inString = !w.inString
		}
	}
	return len(p), nil
}

// Validation precedes the socket handoff. json.Valid checks syntax without
// decoding scalars. The second pass checks duplicate folded keys and the DSM
// envelope using offsets into the same buffer, so even a 20 MiB string is
// never decoded or copied. Offsets fit uint32 under the protocol ceiling.
type webJSONKey struct{ start, end uint32 }

type webJSONValidator struct {
	b       []byte
	i       int
	success bool
	keys    []webJSONKey
}

func validateWebJSON(b []byte) error {
	if !utf8.Valid(b) || !json.Valid(b) || len(b) == 0 || b[0] != '{' {
		return errors.New("WebAPI envelope")
	}
	v := webJSONValidator{b: b}
	// Count keys without decoding strings, then allocate the exact index
	// once. Each key consumes at least five compact bytes, so the index
	// uses at most 8/5 of the payload ceiling (32 MiB). Nested objects reuse
	// this arena; there are no growing per-object maps or slice copies.
	count := 0
	for v.i < len(b) {
		if b[v.i] == '"' {
			v.stringEnd()
			continue
		}
		if b[v.i] == ':' {
			count++
		}
		v.i++
	}
	v.i = 0
	v.keys = make([]webJSONKey, 0, count)
	if err := v.value(true); err != nil {
		return err
	}
	if !v.success {
		return errors.New("WebAPI success missing")
	}
	return nil
}

func (v *webJSONValidator) stringEnd() int {
	v.i++ // opening quote; syntax was already checked
	for {
		c := v.b[v.i]
		v.i++
		if c == '\\' {
			v.i++
		} else if c == '"' {
			return v.i
		}
	}
}

func (v *webJSONValidator) value(root bool) error {
	switch v.b[v.i] {
	case '{':
		v.i++
		startKeys := len(v.keys)
		for v.b[v.i] != '}' {
			start := v.i
			end := v.stringEnd()
			key := webJSONKey{uint32(start + 1), uint32(end - 1)}
			v.keys = append(v.keys, key)
			v.i++ // colon
			if root && compareWebKeys(v.b, key, []byte(`success`), webJSONKey{0, 7}) == 0 {
				v.success = bytes.HasPrefix(v.b[v.i:], []byte("true")) || bytes.HasPrefix(v.b[v.i:], []byte("false"))
			}
			if err := v.value(false); err != nil {
				return err
			}
			if v.b[v.i] == ',' {
				v.i++
			}
		}
		v.i++
		keys := v.keys[startKeys:]
		slices.SortFunc(keys, func(a, b webJSONKey) int { return compareWebKeys(v.b, a, v.b, b) })
		for i := 1; i < len(keys); i++ {
			if compareWebKeys(v.b, keys[i-1], v.b, keys[i]) == 0 {
				return errors.New("duplicate WebAPI key")
			}
		}
		v.keys = v.keys[:startKeys]
	case '[':
		v.i++
		for v.b[v.i] != ']' {
			if err := v.value(false); err != nil {
				return err
			}
			if v.b[v.i] == ',' {
				v.i++
			}
		}
		v.i++
	case '"':
		v.stringEnd()
	default:
		for v.i < len(v.b) && !bytes.ContainsRune([]byte(",]}"), rune(v.b[v.i])) {
			v.i++
		}
	}
	return nil
}

// nextWebKey decodes one key rune, matching encoding/json's handling of
// escaped UTF-16 (including replacement of unpaired surrogates).
func nextWebKey(b []byte, k *webJSONKey) rune {
	i := int(k.start)
	r, n := utf8.DecodeRune(b[i:k.end])
	if r == '\\' {
		i++
		r, n = rune(b[i]), 1
		switch r {
		case 'b':
			r = '\b'
		case 'f':
			r = '\f'
		case 'n':
			r = '\n'
		case 'r':
			r = '\r'
		case 't':
			r = '\t'
		case 'u':
			r, n = webHex(b[i+1:i+5]), 5
			if utf16.IsSurrogate(r) {
				if i+11 <= int(k.end) && b[i+5] == '\\' && b[i+6] == 'u' {
					pair := utf16.DecodeRune(r, webHex(b[i+7:i+11]))
					if pair != utf8.RuneError {
						r, n = pair, 11
					} else {
						r = utf8.RuneError
					}
				} else {
					r = utf8.RuneError
				}
			}
		}
	}
	k.start = uint32(i + n)
	folded := r
	for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
		if next < folded {
			folded = next
		}
	}
	return folded
}

func webHex(b []byte) rune {
	var r rune
	for _, c := range b {
		r <<= 4
		switch {
		case c <= '9':
			r += rune(c - '0')
		case c <= 'F':
			r += rune(c-'A') + 10
		default:
			r += rune(c-'a') + 10
		}
	}
	return r
}

func compareWebKeys(a []byte, ka webJSONKey, b []byte, kb webJSONKey) int {
	for ka.start < ka.end && kb.start < kb.end {
		ra, rb := nextWebKey(a, &ka), nextWebKey(b, &kb)
		if ra < rb {
			return -1
		}
		if ra > rb {
			return 1
		}
	}
	if ka.start < ka.end {
		return 1
	}
	if kb.start < kb.end {
		return -1
	}
	return 0
}

// writeResponse avoids encoding/json's extra full-frame buffer (and HTML
// escaping expansion) for an already validated compact WebAPI envelope.
func writeResponse(w io.Writer, r Response) error {
	if raw, ok := r.Result.(json.RawMessage); r.OK && ok {
		if _, err := io.WriteString(w, "{\"ok\":true,\"result\":"); err != nil {
			return err
		}
		if _, err := w.Write(raw); err != nil {
			return err
		}
		_, err := io.WriteString(w, "}\n")
		return err
	}
	return json.NewEncoder(w).Encode(r)
}
