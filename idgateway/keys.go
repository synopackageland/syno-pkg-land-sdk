package idgateway

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
)

// SigningKey signs ID tokens with RS256, the algorithm every surveyed app accepts.
type SigningKey struct {
	KeyID string
	key   *rsa.PrivateKey
}

// LoadOrCreateSigningKey reads the PEM key at path, creating a 2048-bit key
// with mode 0600 (directory 0700) when it does not exist. A key readable by
// group or others is refused.
func LoadOrCreateSigningKey(path string) (*SigningKey, error) {
	raw, err := readPrivate(path)
	if errors.Is(err, os.ErrNotExist) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		raw = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: must(x509.MarshalPKCS8PrivateKey(key))})
		if err := writePrivate(path, raw); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s: not PEM", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	key, ok := parsed.(*rsa.PrivateKey)
	if err != nil || !ok {
		return nil, fmt.Errorf("%s: not an RSA private key", path)
	}
	sum := sha256.Sum256(must(x509.MarshalPKIXPublicKey(&key.PublicKey)))
	return &SigningKey{KeyID: base64.RawURLEncoding.EncodeToString(sum[:12]), key: key}, nil
}

// Sign returns the compact JWS of claims.
func (k *SigningKey) Sign(claims any) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": k.KeyID})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// JWKS is the public key set served at the jwks endpoint.
func (k *SigningKey) JWKS() []byte {
	pub := k.key.PublicKey
	out, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": k.KeyID,
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
	return out
}

// readPrivate reads a secret file and refuses one others may read.
func readPrivate(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: must be a regular file with mode 0600", path)
	}
	return os.ReadFile(path)
}

// writePrivate creates path with mode 0600 and never replaces an existing file.
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
