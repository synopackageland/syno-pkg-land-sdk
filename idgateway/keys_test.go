package idgateway

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSigningKeyIsCreatedOwnerOnlyAndReused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sso", "signing-key.pem")
	k1, err := LoadOrCreateSigningKey(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", info.Mode(), err)
	}
	k2, err := LoadOrCreateSigningKey(path)
	if err != nil || k1.KeyID != k2.KeyID || k1.key.N.Cmp(k2.key.N) != 0 {
		t.Fatalf("not reused: %v", err)
	}
}

func TestSigningKeyRefusesAGroupOrWorldReadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing-key.pem")
	if _, err := LoadOrCreateSigningKey(path); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0o640)
	if _, err := LoadOrCreateSigningKey(path); err == nil {
		t.Fatal("accepted 0640 key")
	}
}

func TestSignedTokensVerifyAgainstTheJWKS(t *testing.T) {
	k, err := LoadOrCreateSigningKey(filepath.Join(t.TempDir(), "k.pem"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := k.Sign(map[string]any{"sub": "1026"})
	if err != nil {
		t.Fatal(err)
	}
	var set struct {
		Keys []struct{ Kty, Kid, Alg, Use, N, E string }
	}
	json.Unmarshal(k.JWKS(), &set)
	if len(set.Keys) != 1 || set.Keys[0].Kid != k.KeyID || set.Keys[0].Alg != "RS256" || set.Keys[0].Use != "sig" {
		t.Fatalf("jwks %+v", set)
	}
	if !verifyRS256(t, token, set.Keys[0].N, set.Keys[0].E) {
		t.Fatal("signature does not verify")
	}
}

func verifyRS256(t *testing.T, token, n64, e64 string) bool {
	t.Helper()
	parts := strings.Split(token, ".")
	n, _ := base64.RawURLEncoding.DecodeString(n64)
	e, _ := base64.RawURLEncoding.DecodeString(e64)
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) == nil
}
