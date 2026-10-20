package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryResolutionPinsAndVerifiesManifestConfigAndPlatform(t *testing.T) {
	config := []byte(`{"architecture":"amd64","os":"linux","config":{"User":"1000","Volumes":{"/cache":{}}}}`)
	sum := sha256.Sum256(config)
	configDigest := "sha256:" + hex.EncodeToString(sum[:])
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"config":{"digest":%q}}`, configDigest))
	sum = sha256.Sum256(manifest)
	manifestDigest := "sha256:" + hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/manifests/") {
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			w.Write(manifest)
		} else if strings.HasSuffix(r.URL.Path, configDigest) {
			w.Write(config)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	resolver := RegistryResolver{client: srv.Client(), base: func(string) string { return srv.URL }}
	proof, err := resolver.Resolve(context.Background(), "example/test:1", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if proof.Reference != "docker.io/example/test@"+manifestDigest {
		t.Fatal(proof.Reference)
	}
	meta, err := proof.Verify("example/test:1", "x86_64")
	if err != nil || meta.User != "1000" || len(meta.Volumes) != 1 {
		t.Fatal(meta, err)
	}
	if _, err = proof.Verify("example/test:1", "aarch64"); err == nil {
		t.Fatal("wrong architecture accepted")
	}
	var tampered map[string]any
	json.Unmarshal(proof.Config, &tampered)
	tampered["architecture"] = "arm64"
	proof.Config, _ = json.Marshal(tampered)
	if _, err = proof.Verify("example/test:1", "aarch64"); err == nil {
		t.Fatal("tampered config accepted")
	}
}

func TestPinnedSourceDigestCannotBeSilentlySubstituted(t *testing.T) {
	config := []byte(`{"architecture":"amd64","os":"linux","config":{}}`)
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"config":{"digest":%q}}`, contentDigest(config)))
	proof := ImageProof{Reference: "docker.io/example/test@" + contentDigest(manifest), Manifest: manifest, Config: config}
	if _, err := proof.Verify("example/test@sha256:"+strings.Repeat("a", 64), "x86_64"); err == nil {
		t.Fatal("pinned source silently replaced")
	}
}
