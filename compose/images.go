package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/outbound"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/distribution/reference"
)

// ImageProof makes the chosen immutable image and its anonymous-volume
// inventory verifiable during offline SPK inspection. No registry credentials
// or token responses are retained.
type ImageProof struct {
	Reference string `json:"reference"`
	Manifest  []byte `json:"manifest"`
	Config    []byte `json:"config"`
	Index     []byte `json:"index,omitempty"`
}
type ImageMetadata struct {
	User    string
	Volumes []string
}
type imageConfig struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Config       struct {
		User    string                     `json:"User"`
		Volumes map[string]json.RawMessage `json:"Volumes"`
	} `json:"config"`
}
type imageManifest struct {
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		} `json:"platform"`
	} `json:"manifests"`
}

func contentDigest(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}
func imageArchitecture(arch string) string {
	switch arch {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return ""
}
func sameRepository(a, b string) bool {
	x, e := reference.ParseNormalizedNamed(a)
	y, f := reference.ParseNormalizedNamed(b)
	return e == nil && f == nil && reference.TrimNamed(x).Name() == reference.TrimNamed(y).Name()
}
func (p ImageProof) Verify(source, arch string) (ImageMetadata, error) {
	invalid := func() (ImageMetadata, error) {
		return ImageMetadata{}, problem("COMPOSE_IMAGE_LOCK_INVALID", "image proof does not match %s and its requested platform", source)
	}
	if !sameRepository(source, p.Reference) || !strings.HasSuffix(p.Reference, "@"+contentDigest(p.Manifest)) {
		return invalid()
	}
	if named, err := reference.ParseNormalizedNamed(source); err == nil {
		if digested, ok := named.(reference.Digested); ok && digested.Digest().String() != contentDigest(p.Manifest) {
			var index imageManifest
			if contentDigest(p.Index) != digested.Digest().String() || json.Unmarshal(p.Index, &index) != nil {
				return invalid()
			}
			found := false
			for _, item := range index.Manifests {
				if item.Digest == contentDigest(p.Manifest) && item.Platform.OS == "linux" && item.Platform.Architecture == imageArchitecture(arch) {
					found = true
				}
			}
			if !found {
				return invalid()
			}
		}
	}
	var m imageManifest
	var c imageConfig
	if json.Unmarshal(p.Manifest, &m) != nil || m.Config.Digest != contentDigest(p.Config) || json.Unmarshal(p.Config, &c) != nil || c.OS != "linux" || c.Architecture != imageArchitecture(arch) {
		return invalid()
	}
	meta := ImageMetadata{User: c.Config.User}
	for path := range c.Config.Volumes {
		meta.Volumes = append(meta.Volumes, path)
	}
	return meta, nil
}

// RegistryResolver uses anonymous OCI/Docker registry reads. Build credentials
// are never inherited from docker config, environment or the user's keychain.
type RegistryResolver struct {
	Mirrors map[string]string
	client  *http.Client
	base    func(string) string
}

func (r RegistryResolver) Resolve(ctx context.Context, image, arch string) (ImageProof, error) {
	name, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return ImageProof{}, problem("COMPOSE_IMAGE_INVALID", "invalid image %s", image)
	}
	if imageArchitecture(arch) == "" {
		return ImageProof{}, problem("COMPOSE_IMAGE_INVALID", "choose x86_64 or aarch64 before locking images")
	}
	domain, repo := reference.Domain(name), reference.Path(name)
	host := domain
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	base := "https://" + host
	if mirror := r.Mirrors[domain]; mirror != "" {
		u, err := url.Parse(mirror)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return ImageProof{}, problem("COMPOSE_IMAGE_RESOLVE_FAILED", "registry mirror must be an HTTPS origin")
		}
		base = strings.TrimSuffix(mirror, "/")
	}
	if r.base != nil {
		base = r.base(host)
	}
	client := r.client
	if client == nil {
		client = &http.Client{Transport: outbound.NewTransport(30 * time.Second), Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return fmt.Errorf("too many registry redirects")
			}
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
			}
			return nil
		}}
	}
	ref := "latest"
	if tagged, ok := name.(reference.NamedTagged); ok {
		ref = tagged.Tag()
	}
	if digested, ok := name.(reference.Digested); ok {
		ref = digested.Digest().String()
	}
	token := ""
	get := func(path string) ([]byte, error) {
		for attempt := 0; attempt < 2; attempt++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v2/"+repo+path, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
				challenge := resp.Header.Get("WWW-Authenticate")
				resp.Body.Close()
				if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
					return nil, fmt.Errorf("registry requires unsupported authentication")
				}
				attrs := map[string]string{}
				for _, m := range bearerAttribute.FindAllStringSubmatch(challenge, -1) {
					attrs[m[1]] = m[2]
				}
				realm, err := url.Parse(attrs["realm"])
				if err != nil || realm.Scheme != "https" || realm.Host == "" {
					return nil, fmt.Errorf("registry token realm must be HTTPS")
				}
				q := realm.Query()
				q.Set("service", attrs["service"])
				q.Set("scope", "repository:"+repo+":pull")
				realm.RawQuery = q.Encode()
				auth, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
				if err != nil {
					return nil, err
				}
				reply, err := client.Do(auth)
				if err != nil {
					return nil, err
				}
				data, err := io.ReadAll(io.LimitReader(reply.Body, 64<<10))
				reply.Body.Close()
				if err != nil || reply.StatusCode != 200 {
					return nil, fmt.Errorf("anonymous registry authentication failed")
				}
				var body struct {
					Token       string `json:"token"`
					AccessToken string `json:"access_token"`
				}
				if json.Unmarshal(data, &body) != nil {
					return nil, fmt.Errorf("invalid registry token response")
				}
				token = body.Token
				if token == "" {
					token = body.AccessToken
				}
				if token == "" {
					return nil, fmt.Errorf("missing anonymous registry token")
				}
				continue
			}
			data, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
			resp.Body.Close()
			if err != nil {
				return nil, err
			}
			if resp.StatusCode != 200 || len(data) > 16<<20 {
				return nil, fmt.Errorf("registry response status %d or size limit", resp.StatusCode)
			}
			if d := resp.Header.Get("Docker-Content-Digest"); d != "" && d != contentDigest(data) {
				return nil, fmt.Errorf("registry content digest mismatch")
			}
			return data, nil
		}
		return nil, fmt.Errorf("registry authorization failed")
	}
	manifest, err := get("/manifests/" + ref)
	if err != nil {
		return ImageProof{}, problem("COMPOSE_IMAGE_RESOLVE_FAILED", "cannot lock %s: %v", image, err)
	}
	var m imageManifest
	if json.Unmarshal(manifest, &m) != nil {
		return ImageProof{}, problem("COMPOSE_IMAGE_RESOLVE_FAILED", "invalid manifest for %s", image)
	}
	var index []byte
	if len(m.Manifests) > 0 {
		index = manifest
		found := ""
		for _, entry := range m.Manifests {
			if entry.Platform.OS == "linux" && entry.Platform.Architecture == imageArchitecture(arch) {
				found = entry.Digest
				break
			}
		}
		if found == "" {
			return ImageProof{}, problem("COMPOSE_IMAGE_PLATFORM_UNSUPPORTED", "%s has no Linux %s image", image, arch)
		}
		manifest, err = get("/manifests/" + found)
		if err != nil {
			return ImageProof{}, err
		}
		if contentDigest(manifest) != found || json.Unmarshal(manifest, &m) != nil {
			return ImageProof{}, problem("COMPOSE_IMAGE_LOCK_INVALID", "index manifest digest mismatch")
		}
	}
	if !sha256Digest.MatchString(m.Config.Digest) {
		return ImageProof{}, problem("COMPOSE_IMAGE_LOCK_INVALID", "missing immutable image config")
	}
	config, err := get("/blobs/" + m.Config.Digest)
	if err != nil {
		return ImageProof{}, err
	}
	proof := ImageProof{Index: index, Reference: reference.TrimNamed(name).Name() + "@" + contentDigest(manifest), Manifest: manifest, Config: config}
	if _, err = proof.Verify(image, arch); err != nil {
		return ImageProof{}, err
	}
	return proof, nil
}

var bearerAttribute = regexp.MustCompile(`([a-zA-Z_]+)="([^"]*)"`)
var sha256Digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
