package idgateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Subject is a DSM account the authorize CGI verified.
type Subject struct {
	UID   int    `json:"uid"`
	Name  string `json:"name"`
	Admin bool   `json:"admin"`
}

// Assertion carries a verified Subject from the authorize CGI back to the
// gateway transaction Txn, for the app origin Origin only.
type Assertion struct {
	Txn     string  `json:"txn"`
	Origin  string  `json:"origin"`
	Subject Subject `json:"sub"`
	Expires int64   `json:"exp"`
}

// MinKeySize is the shortest HMAC key the gateway and the CGI accept.
const MinKeySize = 32

var errAssertion = errors.New("invalid assertion")

// SealAssertion signs a with key as base64url(JSON) "." base64url(HMAC-SHA256).
func SealAssertion(key []byte, a Assertion) (string, error) {
	if len(key) < MinKeySize {
		return "", errors.New("assertion key too short")
	}
	payload, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(mac(key, body)), nil
}

// OpenAssertion verifies the signature before decoding and refuses an
// assertion that expired at now.
func OpenAssertion(key []byte, sealed string, now time.Time) (Assertion, error) {
	body, sig, ok := strings.Cut(sealed, ".")
	if !ok || len(key) < MinKeySize || len(sealed) > 4096 {
		return Assertion{}, errAssertion
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, mac(key, body)) {
		return Assertion{}, errAssertion
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	var a Assertion
	if err != nil || json.Unmarshal(payload, &a) != nil || now.Unix() > a.Expires {
		return Assertion{}, errAssertion
	}
	return a, nil
}

func mac(key []byte, body string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(body))
	return m.Sum(nil)
}
