// Package compose plans unmodified upstream Compose for DSM's official worker.
// It is shared by build-time tools and the per-package runtime; it never opens
// a Docker socket, starts a process as root, or grants filesystem authority.
package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/distribution/reference"
	"go.yaml.in/yaml/v4"
)

const PlannerVersion = 1
const PackageUser = "${SYNOPKGLAND_UID}:${SYNOPKGLAND_GID}"

// Request contains only package differences; Files are never rewritten.
type Request struct {
	StartupTimeoutSeconds *int `json:"startup_timeout_seconds,omitempty"`

	ResourcePrefix  string                   `json:"resource_prefix,omitempty"`
	Root            string                   `json:"root"`
	Package         string                   `json:"package"`
	Files           []string                 `json:"files"`
	EnvFiles        []string                 `json:"env_files,omitempty"`
	Profiles        []string                 `json:"profiles,omitempty"`
	Web             *Web                     `json:"web,omitempty"`
	Data            map[string]Storage       `json:"data,omitempty"`
	Shares          map[string]Share         `json:"shares,omitempty"`
	Network         Network                  `json:"network"`
	Services        map[string]ServicePolicy `json:"services,omitempty"`
	IdentityMode    string                   `json:"identity_mode,omitempty"`
	ComposeMounts   bool                     `json:"compose_mounts,omitempty"`
	RegistryMirrors map[string]string        `json:"registry_mirrors,omitempty"`
	ResolveImages   bool                     `json:"resolve_images,omitempty"`
	Architecture    string                   `json:"architecture,omitempty"`
	ImageProofs     map[string]ImageProof    `json:"image_proofs,omitempty"`
	ImageDigests    map[string]string        `json:"image_digests,omitempty"`
}
type Web struct {
	Service  string  `json:"service"`
	Port     uint32  `json:"port"`
	HostPort uint32  `json:"host_port"`
	BasePath string  `json:"base_path,omitempty"`
	Health   *Health `json:"health,omitempty"`
}

type Health struct {
	Path   string `json:"path"`
	Status int    `json:"status"`
}

func (w Web) Published() string { return strconv.FormatUint(uint64(w.HostPort), 10) }

type Network struct {
	IngressCandidate int       `json:"ingress_candidate,omitempty"`
	Egress           string    `json:"egress"`
	Allowlist        *[]string `json:"allowlist,omitempty"`
}
type Storage struct {
	Service string `json:"service"`
	Target  string `json:"target"`
	Storage string `json:"storage"`
	Path    string `json:"path,omitempty"`
}
type Mount struct {
	Service string `json:"service"`
	Target  string `json:"target"`
}
type Share struct {
	Select     string  `json:"select"`
	Permission string  `json:"permission"`
	Mounts     []Mount `json:"mounts"`
}
type RunAs struct {
	Mode         string   `json:"mode"`
	Reason       string   `json:"reason,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}
type ServicePolicy struct {
	Listener    *Listener              `json:"listener,omitempty"`
	StorageInit *StorageInitialization `json:"storage_init,omitempty"`
	Environment map[string]string      `json:"environment,omitempty"`
	Egress      string                 `json:"egress,omitempty"`
	Network     string                 `json:"network,omitempty"`
	AddressEnv  string                 `json:"address_env,omitempty"`
	RunAs       *RunAs                 `json:"run_as,omitempty"`
}
type Input struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Content []byte `json:"content,omitempty"`
}
type Image struct {
	Service   string `json:"service"`
	Source    string `json:"source"`
	Reference string `json:"reference"`
	Pinned    bool   `json:"pinned"`
}
type Inventory struct {
	StorageInit    *StorageInitialization      `json:"storage_init,omitempty"`
	Ports          []types.ServicePortConfig   `json:"ports"`
	CapDrop        []string                    `json:"cap_drop"`
	CapAdd         []string                    `json:"cap_add"`
	RootReason     string                      `json:"root_reason,omitempty"`
	HostElevation  bool                        `json:"host_elevation"`
	Retention      string                      `json:"retention"`
	Service        string                      `json:"service"`
	User           string                      `json:"user"`
	ContainerRoot  bool                        `json:"container_root"`
	SDKManaged     bool                        `json:"sdk_managed,omitempty"`
	HostUIDMapping string                      `json:"host_uid_mapping"`
	Egress         string                      `json:"egress"`
	Network        string                      `json:"network"`
	Mounts         []types.ServiceVolumeConfig `json:"mounts"`
}
type AppDataVolume struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}
type AppData struct {
	Project string          `json:"project"`
	Volumes []AppDataVolume `json:"volumes"`
}

type Plan struct {
	AppData               *AppData `json:"app_data,omitempty"`
	StartupTimeoutSeconds int      `json:"startup_timeout_seconds"`

	Ingress       *Ingress              `json:"ingress,omitempty"`
	ImageProofs   map[string]ImageProof `json:"image_proofs,omitempty"`
	ProjectName   string                `json:"project_name"`
	Schema        int                   `json:"schema"`
	Package       string                `json:"package"`
	Inputs        []Input               `json:"inputs"`
	Override      string                `json:"override"`
	Effective     *types.Project        `json:"-"`
	YAML          string                `json:"effective"`
	SHA256        string                `json:"sha256"`
	Images        []Image               `json:"images"`
	Services      []Inventory           `json:"services"`
	Blockers      []string              `json:"acceptance_blockers"`
	MountsBlocked bool                  `json:"mounts_blocked"`
}

var packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{1,63}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var digestImage = regexp.MustCompile(`^\S+@sha256:[a-f0-9]{64}$`)

func problem(code, format string, args ...any) error {
	return fmt.Errorf("[%s] %s", code, fmt.Sprintf(format, args...))
}

// Build loads with the reference Compose loader, applies DSM policy, and emits
// both a true replacement override and the tag-free single worker document.
func Build(ctx context.Context, r Request) (*Plan, error) {
	startup := DefaultStartupTimeoutSeconds
	if r.StartupTimeoutSeconds != nil {
		startup = *r.StartupTimeoutSeconds
	}
	if startup < 2 || startup > MaxStartupTimeoutSeconds {
		return nil, problem("COMPOSE_STARTUP_INVALID", "startup_timeout_seconds must be an integer from 2 to 900 seconds")
	}
	if !packageName.MatchString(r.Package) {
		return nil, problem("COMPOSE_PACKAGE_INVALID", "package must be 2-64 alphanumerics starting with a letter")
	}
	if r.ResourcePrefix != "" && (len(r.ResourcePrefix) > 32 || !regexp.MustCompile(`^[a-z][a-z0-9-]*-$`).MatchString(r.ResourcePrefix)) {
		return nil, problem("COMPOSE_PACKAGE_INVALID", "resource_prefix must be a short lowercase prefix ending in a hyphen")
	}
	if r.Network.IngressCandidate < 0 || r.Network.IngressCandidate > 255 {
		return nil, problem("COMPOSE_SUBNET_INVALID", "ingress_candidate must be 0-255")
	}
	if r.Network.Allowlist != nil || (r.Network.Egress != "isolated" && r.Network.Egress != "internet") {
		return nil, problem("COMPOSE_EGRESS_ALLOWLIST_UNSUPPORTED", "choose isolated or internet; destination allowlists are not implemented")
	}
	if len(r.Files) == 0 || len(r.Files) > 32 {
		return nil, problem("COMPOSE_SOURCE_UNSAFE", "files must contain 1-32 local Compose files")
	}
	if err := sameSourceBase(r.Files); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(r.Root)
	if err != nil {
		return nil, err
	}
	sources := sourceSet{root: root, inputs: map[string]Input{}}
	var files []types.ConfigFile
	for _, name := range r.Files {
		path, b, err := sources.compose(name, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, types.ConfigFile{Filename: path, Content: b})
	}
	env := types.Mapping{}
	for _, name := range r.EnvFiles {
		path, b, err := sources.read(name)
		if err != nil {
			return nil, err
		}
		values, err := readEnvironment(path, b, env)
		if err != nil {
			return nil, err
		}
		for k, v := range values {
			env[k] = v
		}
	}
	// These are owned placeholders, expanded only by DSM materialization.
	for _, k := range []string{"SYNOPKG_PKGVAR", "SYNOPKGLAND_UID", "SYNOPKGLAND_GID"} {
		env[k] = "${" + k + "}"
	}
	project, err := loader.LoadWithContext(ctx, types.ConfigDetails{WorkingDir: filepath.Dir(files[0].Filename), ConfigFiles: files, Environment: env}, func(o *loader.Options) {
		o.SetProjectName(r.ResourcePrefix+strings.ToLower(r.Package), true)
		o.Profiles = r.Profiles
		o.ResolvePaths = true
		o.ResourceLoaders = nil
	})
	if err != nil {
		return nil, problem("COMPOSE_MODEL_INVALID", "%v", err)
	}
	if len(project.Services) == 0 {
		return nil, problem("COMPOSE_MODEL_INVALID", "no active services in the selected profiles")
	}
	for name := range r.Services {
		if _, ok := project.Services[name]; !ok {
			return nil, problem("COMPOSE_SERVICE_UNKNOWN", "services.%s is not active", name)
		}
	}
	if r.Web != nil {
		if _, ok := project.Services[r.Web.Service]; !ok || r.Web.Port == 0 || r.Web.Port > 65535 || (r.Web.HostPort != 0 && (r.Web.HostPort < 1024 || r.Web.HostPort > 65535)) {
			return nil, problem("COMPOSE_WEB_INVALID", "web must name an active service and target port; legacy host_port must be unprivileged")
		}
		if h := r.Web.Health; h != nil {
			u, err := url.ParseRequestURI(h.Path)
			if err != nil || !strings.HasPrefix(h.Path, "/") || strings.HasPrefix(h.Path, "//") || strings.ContainsAny(h.Path, "\r\n#\\") || u.IsAbs() || u.Host != "" || h.Status < 200 || h.Status > 299 {
				return nil, problem("COMPOSE_WEB_INVALID", "health must declare an origin-relative path and expected 2xx status")
			}
		}
		if r.Web.BasePath != "" && r.Web.BasePath != "/" {
			return nil, problem("COMPOSE_BASE_PATH_UNSUPPORTED", "use the upstream's dedicated origin at /; automatic subpath asset rewriting is not supported")
		}
	}
	p := &Plan{ProjectName: r.ResourcePrefix + strings.ToLower(r.Package), Schema: PlannerVersion, Package: r.Package, Effective: project, Blockers: []string{"DSM_DOCKER_PROJECT_MODEL", "DSM_STORAGE_OWNERSHIP_TIMING", "DSM_EGRESS_BOUNDARY"}}
	p.StartupTimeoutSeconds = startup
	if len(r.Shares) > 0 {
		if !r.ComposeMounts {
			return nil, problem("COMPOSE_SHARE_NEEDS_APPROVAL", "declare capabilities.composeMounts: true; user destination grants cannot authorize package mounts")
		}
		p.MountsBlocked = true
		p.Blockers = append(p.Blockers, "COMPOSE_PROJECT_CONTROL_UNAVAILABLE", "DSM_MOUNT_ACL_REVOKE")
	}
	mappings := map[string]Storage{}
	for key, d := range r.Data {
		if _, ok := project.Services[d.Service]; !ok || !containerPath(d.Target) || d.Storage != "package" || !safeRelative(d.Path) {
			return nil, problem("COMPOSE_STORAGE_INVALID", "data.%s needs an active service, target, storage: package and safe path", key)
		}
		id := d.Service + ":" + d.Target
		if _, ok := mappings[id]; ok {
			return nil, problem("COMPOSE_STORAGE_INVALID", "duplicate target %s", id)
		}
		mappings[id] = d
	}
	shared := map[string]string{}
	for key, share := range r.Shares {
		if !envName.MatchString(key) || (share.Select != "admin-after-install" && share.Select != "install-draft") || (share.Permission != "ro" && share.Permission != "rw") || len(share.Mounts) == 0 {
			return nil, problem("COMPOSE_SHARE_INVALID", "shares.%s needs admin selection, ro/rw and fixed mounts", key)
		}
		for _, m := range share.Mounts {
			if _, ok := project.Services[m.Service]; !ok || !containerPath(m.Target) {
				return nil, problem("COMPOSE_SHARE_INVALID", "invalid share target")
			}
			id := m.Service + ":" + m.Target
			if _, ok := mappings[id]; ok {
				return nil, problem("COMPOSE_STORAGE_INVALID", "duplicate target %s", id)
			}
			if _, ok := shared[id]; ok {
				return nil, problem("COMPOSE_STORAGE_INVALID", "duplicate target %s", id)
			}
			shared[id] = key
		}
	}
	for name, n := range project.Networks {
		if name == "sdk_outbound" {
			return nil, problem("COMPOSE_NETWORK_UNSUPPORTED", "sdk_outbound is reserved for SDK outbound transport")
		}
		if n.External || (n.Driver != "" && n.Driver != "bridge") || len(n.DriverOpts) > 0 || n.Ipam.Driver != "" || len(n.Ipam.Config) > 0 {
			return nil, problem("COMPOSE_NETWORK_UNSUPPORTED", "network %s must be a managed bridge without custom driver/IPAM options", name)
		}
		n.Name = r.ResourcePrefix + strings.ToLower(r.Package) + "_" + name
		n.Internal = true
		n.Attachable = false
		project.Networks[name] = n
	}
	directIngress, err := addDirectIngress(project, r)
	if err != nil {
		return nil, err
	}
	p.Ingress = directIngress
	used := map[string]bool{}
	for _, name := range project.ServiceNames() {
		s := project.Services[name]
		policy := r.Services[name]
		if err := validateListener(policy); err != nil {
			return nil, err
		}
		managedIngress := false
		if err := validateService(s); err != nil {
			return nil, err
		}
		egress := policy.Egress
		if egress == "" {
			egress = r.Network.Egress
		}
		if egress != "isolated" && egress != "internet" {
			return nil, problem("COMPOSE_EGRESS_ALLOWLIST_UNSUPPORTED", "service %s must use isolated or internet", name)
		}
		s.Profiles = nil
		s.ContainerName = ""
		s.Ports = nil
		s.EnvFiles = nil
		s.LabelFiles = nil
		s.GroupAdd = nil
		s.User = PackageUser
		s.CapDrop = []string{"ALL"}
		s.CapAdd = nil
		s.SecurityOpt = []string{"no-new-privileges:true"}
		if s.NetworkMode == "host" && policy.Network != "host" {
			return nil, problem("COMPOSE_NETWORK_UNSUPPORTED", "upstream host networking on %s requires an explicit verified topology or upstream alternative", name)
		}
		s.NetworkMode = ""
		if s.Networks == nil {
			s.Networks = map[string]*types.ServiceNetworkConfig{"default": nil}
		}
		if policy.Network == "host" {
			if r.IdentityMode != "oidc" || r.Web == nil || r.Web.Service != name || policy.AddressEnv == "" && policy.Listener == nil {
				return nil, problem("COMPOSE_OIDC_TOPOLOGY_UNSUPPORTED", "host service %s requires oidc Web ingress and a verified address_env", name)
			}
			if r.Web.HostPort != 0 && r.Web.HostPort != r.Web.Port {
				return nil, problem("COMPOSE_OIDC_TOPOLOGY_UNSUPPORTED", "host ingress cannot remap ports; web host_port must equal target port")
			}
			if egress != "internet" {
				return nil, problem("COMPOSE_OIDC_TOPOLOGY_UNSUPPORTED", "host networking on %s cannot enforce isolated egress; explicitly choose internet", name)
			}
			s.NetworkMode = "host"
			s.Networks = nil
			egress = "internet"
			applyListener(&s, policy, r.Web.Port)
		} else if policy.Network != "" && policy.Network != "bridge" {
			return nil, problem("COMPOSE_NETWORK_UNSUPPORTED", "service %s network must be bridge or host", name)
		}
		if r.IdentityMode == "oidc" && r.Web != nil && r.Web.Service == name && s.NetworkMode != "host" {
			return nil, problem("COMPOSE_OIDC_TOPOLOGY_UNSUPPORTED", "the embedded issuer is NAS loopback; declare verified host networking for its consumer")
		}

		if s.NetworkMode != "host" && egress == "internet" && !managedIngress {
			if project.Networks == nil {
				project.Networks = types.Networks{}
			}
			project.Networks["sdk_outbound"] = types.NetworkConfig{Name: r.ResourcePrefix + strings.ToLower(r.Package) + "_sdk_outbound"}
			s.Networks["sdk_outbound"] = nil
		}
		for key, value := range policy.Environment {
			if !envName.MatchString(key) || strings.ContainsRune(value, 0) {
				return nil, problem("COMPOSE_ENV_INVALID", "invalid service environment")
			}
			setEnv(&s, key, value)
		}
		// The bootstrap adapter and the supervisor share one declared budget.
		// Set this after upstream and manifest env so neither can desynchronize it.
		setEnv(&s, StartupBudgetEnv, strconv.Itoa(startup))
		for key, value := range s.Environment {
			if value == nil {
				return nil, problem("COMPOSE_ENV_INVALID", "%s environment %s is unset; declare its value in a local env file or service policy", name, key)
			}
		}
		if egress == "isolated" {
			s.DNS = []string{"127.0.0.1"}
		}
		if s.NetworkMode == "host" {
			applyListener(&s, policy, r.Web.Port)
		}
		rootContainer := policy.RunAs != nil && policy.RunAs.Mode == "container-root"
		if policy.RunAs != nil && policy.RunAs.Mode != "package" && !rootContainer {
			return nil, problem("COMPOSE_RUN_AS_INVALID", "service %s run_as must be package or container-root", name)
		}
		if rootContainer {
			if strings.TrimSpace(policy.RunAs.Reason) == "" || s.NetworkMode == "host" {
				return nil, problem("COMPOSE_ROOT_OPT_IN_INVALID", "container root needs a reason and bridge networking")
			}
			s.User = "0:0"
			for _, c := range policy.RunAs.Capabilities {
				if !slices.Contains([]string{"CHOWN", "FOWNER", "SETUID", "SETGID"}, c) {
					return nil, problem("COMPOSE_PRIVILEGE_UNSUPPORTED", "container root capability %s is not supported", c)
				}
			}
			s.CapAdd = policy.RunAs.Capabilities
		}
		for i, v := range s.Volumes {
			id := name + ":" + v.Target
			if d, ok := mappings[id]; ok {
				v = types.ServiceVolumeConfig{Type: "bind", Source: "${SYNOPKG_PKGVAR}/" + d.Path, Target: d.Target, Bind: &types.ServiceVolumeBind{CreateHostPath: false}}
				used[id] = true
			} else if key, ok := shared[id]; ok {
				v = types.ServiceVolumeConfig{Type: "bind", Source: "${SYNOPKGLAND_MOUNT_" + key + "}", Target: v.Target, ReadOnly: r.Shares[key].Permission == "ro", Bind: &types.ServiceVolumeBind{CreateHostPath: false}}
				used[id] = true
			} else {
				switch v.Type {
				case "volume":
					if v.Source == "" {
						return nil, problem("COMPOSE_ANONYMOUS_VOLUME_UNSUPPORTED", "%s:%s needs a stable declared volume", name, v.Target)
					}
				case "tmpfs":
				case "bind":
					if !(v.ReadOnly && (v.Source == "/etc/localtime" && v.Target == "/etc/localtime" || v.Source == "/etc/timezone" && v.Target == "/etc/timezone")) {
						return nil, problem("COMPOSE_STORAGE_UNMAPPED", "%s:%s needs package data or an approved Share mapping", name, v.Target)
					}
				default:
					return nil, problem("COMPOSE_STORAGE_UNMAPPED", "unsupported %s mount on %s", v.Type, name)
				}
			}
			if rootContainer && v.Type == "bind" {
				return nil, problem("COMPOSE_ROOT_HOST_MOUNT_FORBIDDEN", "container-root service %s cannot bind host files or Shares", name)
			}
			if !containerPath(v.Target) {
				return nil, problem("COMPOSE_STORAGE_INVALID", "unsafe target %s", v.Target)
			}
			s.Volumes[i] = v
		}
		for id, d := range mappings {
			if d.Service == name && !used[id] {
				s.Volumes = append(s.Volumes, types.ServiceVolumeConfig{Type: "bind", Source: "${SYNOPKG_PKGVAR}/" + d.Path, Target: d.Target, Bind: &types.ServiceVolumeBind{CreateHostPath: false}})
				used[id] = true
				if rootContainer {
					return nil, problem("COMPOSE_ROOT_HOST_MOUNT_FORBIDDEN", "container root cannot bind package data")
				}
			}
		}
		for _, share := range r.Shares {
			for _, m := range share.Mounts {
				id := m.Service + ":" + m.Target
				if m.Service == name && !used[id] {
					s.Volumes = append(s.Volumes, types.ServiceVolumeConfig{Type: "bind", Source: "${SYNOPKGLAND_MOUNT_" + shared[id] + "}", Target: m.Target, ReadOnly: share.Permission == "ro", Bind: &types.ServiceVolumeBind{CreateHostPath: false}})
					used[id] = true
					if rootContainer {
						return nil, problem("COMPOSE_ROOT_HOST_MOUNT_FORBIDDEN", "container root cannot bind Shares")
					}
				}
			}
		}
		original := s.Image
		proof, hasProof := r.ImageProofs[original]
		if r.ResolveImages && !hasProof {
			var e error
			proof, e = (RegistryResolver{Mirrors: r.RegistryMirrors}).Resolve(ctx, original, r.Architecture)
			if e != nil {
				return nil, e
			}
			hasProof = true
		}
		if hasProof {
			meta, e := proof.Verify(original, r.Architecture)
			if e != nil {
				return nil, e
			}
			if p.ImageProofs == nil {
				p.ImageProofs = map[string]ImageProof{}
			}
			p.ImageProofs[original] = proof
			s.Image = proof.Reference
			slices.Sort(meta.Volumes)
			for _, target := range meta.Volumes {
				target = filepath.Clean(target)
				if !containerPath(target) {
					return nil, problem("COMPOSE_IMAGE_LOCK_INVALID", "unsafe image VOLUME target")
				}
				covered := false
				for _, v := range s.Volumes {
					if filepath.Clean(v.Target) == target {
						covered = true
					}
				}
				if !covered {
					sum := sha256.Sum256([]byte(name + ":" + target))
					key := "sdk_image_" + hex.EncodeToString(sum[:6])
					if project.Volumes == nil {
						project.Volumes = types.Volumes{}
					}
					if _, exists := project.Volumes[key]; exists {
						return nil, problem("COMPOSE_VOLUME_DRIVER_UNSUPPORTED", "reserved image volume namespace collision")
					}
					project.Volumes[key] = types.VolumeConfig{}
					s.Volumes = append(s.Volumes, types.ServiceVolumeConfig{Type: "volume", Source: key, Target: target})
				}
			}
		}
		if pinned, ok := r.ImageDigests[original]; ok {
			if hasProof && pinned != proof.Reference {
				return nil, problem("COMPOSE_IMAGE_LOCK_INVALID", "pin differs from verified metadata for %s", name)
			}
			originalName, originalErr := reference.ParseNormalizedNamed(original)
			pinnedName, pinnedErr := reference.ParseNormalizedNamed(pinned)
			if originalErr != nil || pinnedErr != nil || reference.TrimNamed(originalName).Name() != reference.TrimNamed(pinnedName).Name() || !digestImage.MatchString(pinned) {
				return nil, problem("COMPOSE_IMAGE_LOCK_INVALID", "invalid digest for %s", name)
			}
			s.Image = pinned
		}
		if policy.StorageInit != nil && policy.StorageInit.Runtime != nil {
			for _, e := range policy.StorageInit.Runtime.Environment {
				if e.OIDCClient != "" && r.IdentityMode != "oidc" {
					return nil, problem("COMPOSE_STORAGE_INIT_INVALID", "OIDC runtime environment requires oidc identity mode")
				}
			}
		}
		if err := applyStorageInit(r.Package, &s, policy.StorageInit, rootContainer); err != nil {
			return nil, err
		}
		p.Images = append(p.Images, Image{Service: name, Source: original, Reference: s.Image, Pinned: digestImage.MatchString(s.Image)})
		project.Services[name] = s
		reason := ""
		if rootContainer {
			reason = policy.RunAs.Reason
		}
		p.Services = append(p.Services, Inventory{StorageInit: policy.StorageInit, SDKManaged: managedIngress, Ports: s.Ports, CapDrop: s.CapDrop, CapAdd: s.CapAdd, RootReason: reason, HostElevation: false, Retention: "retain", Service: name, User: s.User, ContainerRoot: rootContainer, HostUIDMapping: "unknown", Egress: egress, Network: func() string {
			if s.NetworkMode == "host" {
				return "host"
			}
			return "bridge"
		}(), Mounts: s.Volumes})
	}
	if len(project.Volumes) > 256 {
		return nil, problem("COMPOSE_VOLUME_DRIVER_UNSUPPORTED", "at most 256 App data volumes are supported")
	}
	for name, v := range project.Volumes {
		if len(name) > 128 || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(name) {
			return nil, problem("COMPOSE_VOLUME_DRIVER_UNSUPPORTED", "volume key is outside the bounded App data contract")
		}
		if v.External {
			return nil, problem("COMPOSE_EXTERNAL_VOLUME_UNVERIFIED", "external volume %s needs trusted admin ownership/driver readback; no official controller is configured", name)
		}
		if (v.Driver != "" && v.Driver != "local") || len(v.DriverOpts) > 0 {
			return nil, problem("COMPOSE_VOLUME_DRIVER_UNSUPPORTED", "volume %s cannot hide a host/remote mount or plugin", name)
		}
		v.Name = r.ResourcePrefix + strings.ToLower(r.Package) + "_" + name
		project.Volumes[name] = v
	}
	{
		p.AppData = &AppData{Project: p.ProjectName, Volumes: []AppDataVolume{}}
		for _, name := range slices.Sorted(maps.Keys(project.Volumes)) {
			p.AppData.Volumes = append(p.AppData.Volumes, AppDataVolume{Key: name, Name: project.Volumes[name].Name})
		}
	}
	// Config/secret resources require an immutable private source inventory; reject
	// until that contract is implemented rather than leaking arbitrary host files.
	if len(project.Secrets) > 0 || len(project.Configs) > 0 {
		return nil, problem("COMPOSE_FILE_RESOURCE_UNSUPPORTED", "secret/config resource delivery is not implemented; use explicit package-owned configuration")
	}
	raw, err := project.MarshalYAML()
	if err != nil {
		return nil, err
	}
	raw, err = workerYAML(raw)
	if err != nil {
		return nil, err
	}
	p.YAML = string(raw)
	sum := sha256.Sum256(raw)
	p.SHA256 = hex.EncodeToString(sum[:])
	p.Override, err = replacementOverride(raw)
	if err != nil {
		return nil, err
	}
	// Preserve ordered primary sources first, then dependency snapshots in a stable order.
	seen := map[string]bool{}
	for _, name := range r.Files {
		rel, _ := filepath.Rel(root, filepath.Join(root, name))
		p.Inputs = append(p.Inputs, sources.inputs[rel])
		seen[rel] = true
	}
	names := make([]string, 0)
	for name := range sources.inputs {
		if !seen[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		p.Inputs = append(p.Inputs, sources.inputs[name])
	}
	return p, nil
}
func setEnv(s *types.ServiceConfig, key, value string) {
	if s.Environment == nil {
		s.Environment = types.MappingWithEquals{}
	}
	s.Environment[key] = &value
}
func safeRelative(s string) bool {
	return s != "" && !filepath.IsAbs(s) && !strings.ContainsAny(s, "\\\x00") && filepath.Clean(s) == s && s != "." && !slices.Contains(strings.Split(s, "/"), "..")
}
func containerPath(s string) bool {
	return strings.HasPrefix(s, "/") && s != "/" && !strings.ContainsAny(s, "\\\x00") && filepath.Clean(s) == s
}

func validateService(s types.ServiceConfig) error {
	if s.Build != nil || s.Dockerfile != "" {
		return problem("COMPOSE_BUILD_UNSUPPORTED", "%s must use an upstream prebuilt image", s.Name)
	}
	if s.Image == "" {
		return problem("COMPOSE_IMAGE_INVALID", "service %s needs an image", s.Name)
	}
	if s.Privileged || s.Pid != "" || s.Ipc != "" || s.Uts != "" || s.UserNSMode != "" || s.Cgroup != "" || s.CgroupParent != "" || len(s.CapAdd) > 0 || len(s.Devices) > 0 || len(s.DeviceCgroupRules) > 0 || len(s.Gpus) > 0 || len(s.VolumesFrom) > 0 || len(s.GroupAdd) > 0 || s.UseAPISocket || s.CredentialSpec != nil || len(s.PreStart) > 0 || len(s.PostStart) > 0 || len(s.PreStop) > 0 || len(s.Sysctls) > 0 || s.Provider != nil || s.Develop != nil || s.Models != nil || s.Runtime != "" || len(s.ExternalLinks) > 0 || len(s.Links) > 0 || len(s.ExtraHosts) > 0 || len(s.StorageOpt) > 0 || s.VolumeDriver != "" || s.Isolation != "" {
		return problem("COMPOSE_PRIVILEGE_UNSUPPORTED", "%s uses unsupported host access, capabilities, devices, hooks or runtime", s.Name)
	}
	for _, opt := range s.SecurityOpt {
		if opt != "no-new-privileges:true" && opt != "no-new-privileges" {
			return problem("COMPOSE_PRIVILEGE_UNSUPPORTED", "%s security_opt %s is not supported", s.Name, opt)
		}
	}
	if s.GetScale() != 1 || s.Deploy != nil {
		return problem("COMPOSE_SCALE_UNSUPPORTED", "%s deploy/replicas need an explicit local runtime contract", s.Name)
	}
	if s.NetworkMode != "" && s.NetworkMode != "bridge" && s.NetworkMode != "host" {
		return problem("COMPOSE_NETWORK_UNSUPPORTED", "unsupported network_mode on %s", s.Name)
	}
	return nil
}

// Replacement tags are used in the review artifact only, never in the worker
// document. A normal ports merge would retain the original wildcard publishes.
func replacementOverride(raw []byte) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	root := doc.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value != "services" {
			continue
		}
		services := root.Content[i+1]
		for j := 1; j < len(services.Content); j += 2 {
			s := services.Content[j]
			for _, key := range []string{"ports", "volumes", "networks", "cap_drop", "cap_add", "security_opt", "env_file", "group_add", "profiles", "dns"} {
				var n *yaml.Node
				for k := 0; k < len(s.Content); k += 2 {
					if s.Content[k].Value == key {
						n = s.Content[k+1]
						break
					}
				}
				if n == nil {
					n = &yaml.Node{Kind: yaml.SequenceNode}
					s.Content = append(s.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, n)
				}
				n.Tag = "!override"
			}
			for _, key := range []string{"container_name", "network_mode"} {
				found := false
				for k := 0; k < len(s.Content); k += 2 {
					if s.Content[k].Value == key {
						found = true
					}
				}
				if !found {
					s.Content = append(s.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!reset", Value: "null"})
				}
			}
		}
	}
	b, err := yaml.Marshal(&doc)
	return string(b), err
}

// LockJSON excludes source bytes, generated environment and secret values.
func (p *Plan) LockJSON() ([]byte, error) {
	inputs := make([]Input, len(p.Inputs))
	for i, in := range p.Inputs {
		inputs[i] = Input{Path: in.Path, SHA256: in.SHA256}
	}
	return json.MarshalIndent(struct {
		AppData       *AppData    `json:"app_data,omitempty"`
		Ingress       *Ingress    `json:"ingress,omitempty"`
		ProjectName   string      `json:"project_name"`
		Schema        int         `json:"schema"`
		Package       string      `json:"package"`
		Inputs        []Input     `json:"inputs"`
		SHA256        string      `json:"effective_sha256"`
		Images        []Image     `json:"images"`
		Services      []Inventory `json:"services"`
		Blockers      []string    `json:"acceptance_blockers"`
		MountsBlocked bool        `json:"mounts_blocked"`
	}{p.AppData, p.Ingress, p.ProjectName, p.Schema, p.Package, inputs, p.SHA256, p.Images, p.Services, p.Blockers, p.MountsBlocked}, "", "  ")
}
