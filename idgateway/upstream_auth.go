package idgateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// UpstreamAuthPathsVersion identifies the static upstream authentication contract.
const UpstreamAuthPathsVersion = 1

// Set by the package builder. Bind every gateway admission shortcut to the binary.
var expectedEntryPolicyDigest string

var upstreamPrefixPattern = regexp.MustCompile(`^/[A-Za-z0-9._~/-]+$`)

func validUpstreamPrefix(p string) bool {
	return p != "/" && len(p) <= 512 && upstreamPrefixPattern.MatchString(p) &&
		cleanPath(p) && !strings.Contains(p, "//") && !pathPrefix(p, "/_sso") && !pathPrefix(p, "/_spl")
}

func validateUpstreamAuth(paths, permanent []string, optional map[string]OptionalPassthroughRule) error {
	if paths == nil {
		return nil
	}
	if len(paths) == 0 || len(paths) > 16 {
		return errors.New("upstream_auth_paths needs 1-16 fixed prefixes")
	}
	for i, p := range paths {
		if !validUpstreamPrefix(p) {
			return fmt.Errorf("invalid upstream_auth_paths prefix %q", p)
		}
		for _, other := range paths[:i] {
			if pathPrefix(p, other) || pathPrefix(other, p) {
				return errors.New("upstream_auth_paths prefixes must not overlap")
			}
		}
	}
	passthrough := append([]string(nil), permanent...)
	for _, rule := range optional {
		passthrough = append(passthrough, rule.Prefixes...)
	}
	for _, p := range passthrough {
		covered := false
		for _, prefix := range paths {
			covered = covered || pathPrefix(p, prefix)
		}
		if !validUpstreamPrefix(p) || !covered {
			return errors.New("passthrough must be contained in upstream_auth_paths")
		}
	}
	return nil
}

func (f *Front) upstreamAuthenticated(r *http.Request) bool {
	for _, prefix := range f.cfg.UpstreamAuthPaths {
		if pathPrefix(r.URL.Path, prefix) {
			return true
		}
	}
	return false
}

// EntryPolicyDigest binds static admission settings, including upstream-owned
// paths. Empty collections have one representation across SDK and CLI builds.
func (c Config) EntryPolicyDigest() string {
	paths, permanent, optional, groups := c.UpstreamAuthPaths, c.Passthrough, c.OptionalPassthrough, c.Groups
	if paths == nil {
		paths = []string{}
	}
	if permanent == nil {
		permanent = []string{}
	}
	if optional == nil {
		optional = map[string]OptionalPassthroughRule{}
	}
	if groups == nil {
		groups = []string{}
	}
	raw, _ := json.Marshal(struct {
		Entry    string                             `json:"entry"`
		Access   string                             `json:"access"`
		Upstream string                             `json:"upstream_access"`
		Groups   []string                           `json:"groups"`
		Blocked  bool                               `json:"compose_blocked"`
		Paths    []string                           `json:"upstream_auth_paths"`
		Pass     []string                           `json:"passthrough"`
		Optional map[string]OptionalPassthroughRule `json:"optional_passthrough"`
	}{c.Entry, c.Access, c.UpstreamAccess, groups, c.ComposeBlocked, paths, permanent, optional})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (c Config) checkEntryPolicy() error {
	if expectedEntryPolicyDigest != "" && c.EntryPolicyDigest() != expectedEntryPolicyDigest {
		return errors.New("gateway entry policy does not match package build policy")
	}
	return nil
}
