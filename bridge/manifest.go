package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
)

// ManifestFile is the manifest's name in the App directory.
const ManifestFile = "bridge.json"

// MaxManifestBytes bounds bridge.json.
const MaxManifestBytes = 1 << 20

// Manifest is bridge.json (spec/bridge/v1/manifest.schema.json): what the
// package build validated about the App. Later versions add members; v1
// refuses any it does not know.
type Manifest struct {
	Schema          int               `json:"schema"`
	Runtime         string            `json:"runtime"`
	Entry           string            `json:"entry"`
	Mode            string            `json:"mode"`
	Methods         []string          `json:"methods"`
	Access          string            `json:"access,omitempty"`
	TimeoutSeconds  int               `json:"timeout_seconds,omitempty"`
	MaxRequestBytes int64             `json:"max_request_bytes,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Node            *NodeRuntime      `json:"node,omitempty"`
	Transfers       map[string]string `json:"transfers,omitempty"`
	MaxUploadBytes  int64             `json:"max_upload_bytes,omitempty"`
	Capabilities    Capabilities      `json:"capabilities,omitzero"`
}

// NodeRuntime names the Package Center Node.js package the App runs on.
type NodeRuntime struct {
	Package string `json:"package"`
}

// Capabilities are SDK routes the runtime serves in Go, never through the App.
type Capabilities struct {
	WebAPI   bool   `json:"webapi,omitempty"`   // Profile grants and JSON calls through the executor.
	Files    string `json:"files,omitempty"`    // "session": nas.* with the caller's DSM session
	Identity string `json:"identity,omitempty"` // "standalone": the SDK's App sign-in
	Portal   bool   `json:"portal,omitempty"`   // portal.status
	Jobs     bool   `json:"jobs,omitempty"`     // the bg.* services on the package's job store
	// Root forwards the root.* services to the package's executor with the
	// request's DSM session; the executor's policy decides. It also serves
	// the browser's executor.status and executor.control.
	Root bool `json:"root,omitempty"`
	// Delegate serves delegate.* with the package's executor: the routes
	// for the browser and the services for the App.
	Delegate *DelegateCapability `json:"delegate,omitempty"`
}

// DelegateCapability is the package's executor integration for saving to a
// destination the user chose: lifetime until-revoked, operation
// files.moveFromStaging.
type DelegateCapability struct {
	Scope          string   `json:"scope"`
	Destinations   []string `json:"destinations"`
	ExecutorDigest string   `json:"executor_digest"`
	PolicyDigest   string   `json:"policy_digest"`
}

var (
	entryPath   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,255}$`)
	envName     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	nodePackage = regexp.MustCompile(`^Node\.js_v[0-9]{2}$`)
	scope       = regexp.MustCompile(`^(home|install-selected|shared:[^/\\:\x00-\x1f]{1,255})$`)
	destination = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	digest      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ParseManifest reads bridge.json and refuses anything the runtime could not
// honour exactly.
func ParseManifest(raw []byte) (Manifest, error) {
	var m Manifest
	if len(raw) > MaxManifestBytes {
		return m, errors.New("bridge.json is too large")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil || dec.More() {
		return Manifest{}, fmt.Errorf("bridge.json: %v", err)
	}
	if err := m.check(); err != nil {
		return Manifest{}, fmt.Errorf("bridge.json: %w", err)
	}
	return m, nil
}

func (m Manifest) check() error {
	switch {
	case m.Schema != 1:
		return fmt.Errorf("schema %d, want 1", m.Schema)
	case m.Runtime != "python" && m.Runtime != "node" && m.Runtime != "rust":
		return fmt.Errorf("unknown runtime %q", m.Runtime)
	case m.Mode == "socket":
		return errors.New(`mode "socket" is reserved; v1 runs Apps in mode "exec"`)
	case m.Mode != "exec":
		return fmt.Errorf("unknown mode %q", m.Mode)
	case !entryPath.MatchString(m.Entry) || path.IsAbs(m.Entry) || path.Clean(m.Entry) != m.Entry || m.Entry == "." || m.Entry == ".." || strings.HasPrefix(m.Entry, "../"):
		return fmt.Errorf("entry %q must be a clean path inside the App directory", m.Entry)
	case m.Access != "" && m.Access != dsm.EntryAdminOnly && m.Access != dsm.EntryAppPrivilege:
		return fmt.Errorf("unknown access %q", m.Access)
	case m.TimeoutSeconds < 0 || m.TimeoutSeconds > 3600:
		return fmt.Errorf("timeout_seconds %d is outside 1..3600", m.TimeoutSeconds)
	case m.MaxRequestBytes < 0 || m.MaxRequestBytes > api.MaxRequestBytesCeiling:
		return fmt.Errorf("max_request_bytes %d is outside 1..%d", m.MaxRequestBytes, api.MaxRequestBytesCeiling)
	case (m.Runtime == "node") != (m.Node != nil):
		return errors.New("node is required for runtime node and only for it")
	case m.Node != nil && !nodePackage.MatchString(m.Node.Package):
		return fmt.Errorf("node package %q", m.Node.Package)
	case m.Capabilities.Files != "" && m.Capabilities.Files != "session":
		return fmt.Errorf("unknown files capability %q", m.Capabilities.Files)
	case m.Capabilities.Identity != "" && m.Capabilities.Identity != "standalone":
		return fmt.Errorf("unknown identity capability %q", m.Capabilities.Identity)
	}
	if err := checkEnv(m.Env); err != nil {
		return err
	}
	if err := m.checkTransfers(); err != nil {
		return err
	}
	if err := m.Capabilities.Delegate.check(); err != nil {
		return err
	}
	return checkMethods(m.Methods)
}

func (m Manifest) checkTransfers() error {
	if m.MaxUploadBytes < 0 || m.MaxUploadBytes > MaxUploadBytes {
		return fmt.Errorf("max_upload_bytes %d is outside 1..%d", m.MaxUploadBytes, MaxUploadBytes)
	}
	for method, kind := range m.Transfers {
		if kind != "upload" && kind != "download" || !slices.Contains(m.Methods, method) {
			return fmt.Errorf("transfer %s: %q, want upload or download of a listed method", method, kind)
		}
	}
	return nil
}

func (d *DelegateCapability) check() error {
	if d == nil {
		return nil
	}
	if !scope.MatchString(d.Scope) || !digest.MatchString(d.ExecutorDigest) || !digest.MatchString(d.PolicyDigest) {
		return errors.New("delegate: scope or digests invalid")
	}
	if len(d.Destinations) == 0 || len(d.Destinations) > 64 {
		return errors.New("delegate: destinations must list 1..64 keys")
	}
	seen := map[string]bool{}
	for _, key := range d.Destinations {
		if !destination.MatchString(key) || seen[key] {
			return fmt.Errorf("delegate: destination %q is invalid or listed twice", key)
		}
		seen[key] = true
	}
	return nil
}

func checkEnv(env map[string]string) error {
	if len(env) > 64 {
		return errors.New("env has more than 64 names")
	}
	for name, value := range env {
		reserved := name == "PATH"
		for _, prefix := range []string{"HTTP_", "SYNOPKGLAND_", "LD_", "DYLD_"} {
			reserved = reserved || strings.HasPrefix(name, prefix)
		}
		if !envName.MatchString(name) || reserved {
			return fmt.Errorf("env name %q is invalid or reserved", name)
		}
		if len(value) > 4096 || strings.ContainsRune(value, 0) {
			return fmt.Errorf("env %s: value too long or holds NUL", name)
		}
	}
	return nil
}

func checkMethods(methods []string) error {
	if err := api.ValidateAppMethods(methods); err != nil {
		return fmt.Errorf("methods must be App methods, not SDK ones: %v", err)
	}
	return nil
}
