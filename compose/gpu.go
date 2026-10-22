package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Devices grants only SDK-defined device classes, never caller-provided paths/GIDs.
type Devices struct {
	GPU *GPU `json:"gpu,omitempty"`
}
type GPU struct {
	Services []string `json:"services"`
}
type DeviceInventory struct {
	GPU *GPUInventory `json:"gpu,omitempty"`
}
type GPUInventory struct {
	Services  []string `json:"services"`
	Resource  string   `json:"resource"`
	GIDSource string   `json:"gid_source"`
	Paths     string   `json:"paths"`
	Fallback  string   `json:"fallback"`
}

func planDevices(r Request) (*DeviceInventory, error) {
	if r.Devices == nil {
		return nil, nil
	}
	if r.Devices.GPU == nil || len(r.Devices.GPU.Services) == 0 || len(r.Devices.GPU.Services) > 16 {
		return nil, problem("COMPOSE_GPU_INVALID", "devices requires gpu.services with 1-16 active package-UID services")
	}
	seen := map[string]bool{}
	for _, name := range r.Devices.GPU.Services {
		policy := r.Services[name]
		if seen[name] || policy.RunAs != nil && (policy.RunAs.Mode != "package" || len(policy.RunAs.Capabilities) > 0) {
			return nil, problem("COMPOSE_GPU_INVALID", "GPU services must be distinct and use the package UID without added capabilities")
		}
		seen[name] = true
	}
	return &DeviceInventory{GPU: &GPUInventory{Services: slices.Clone(r.Devices.GPU.Services), Resource: "video-driver", GIDSource: "videodriver", Paths: "Intel /dev/dri/renderD128-191 (runtime detected)", Fallback: "software"}}, nil
}

type gpuNode struct {
	Path, Vendor       string
	Major, Minor, GID  uint32
	Mode               os.FileMode
	Character, Symlink bool
}

var renderNode = regexp.MustCompile(`^/dev/dri/renderD(1[2-8][0-9]|19[01])$`)

// materializeGPU is deliberately independent of the build host. Only runtime
// discovery can supply device metadata; the manifest cannot supply a GID/path.
func materializeGPU(raw []byte, inventory *DeviceInventory, nodes []gpuNode, gid int) ([]byte, error) {
	if inventory == nil || inventory.GPU == nil {
		return raw, nil
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	services, ok := doc["services"].(map[string]any)
	if !ok {
		return nil, problem("COMPOSE_LOCK_INVALID", "GPU template services missing")
	}
	var devices []string
	for _, n := range nodes {
		minor, err := strconv.Atoi(strings.TrimPrefix(n.Path, "/dev/dri/renderD"))
		if err == nil && renderNode.MatchString(n.Path) && minor >= 128 && minor <= 191 && uint32(minor) == n.Minor && n.Major == 226 && n.Character && !n.Symlink && n.Vendor == "0x8086" && gid > 0 && n.GID == uint32(gid) && n.Mode.Perm()&0060 == 0060 {
			devices = append(devices, n.Path+":"+n.Path+":rw")
		}
	}
	slices.Sort(devices)
	devices = slices.Compact(devices)
	if len(devices) > 16 {
		devices = nil
	} // An unexpected host topology cannot expand authority.
	for _, name := range inventory.GPU.Services {
		service, ok := services[name].(map[string]any)
		if !ok || service["user"] != PackageUser {
			return nil, problem("COMPOSE_LOCK_INVALID", "GPU needs a package-UID service")
		}
		if service["privileged"] == true || service["cap_add"] != nil {
			return nil, problem("COMPOSE_GPU_INVALID", "GPU cannot elevate a container")
		}
		delete(service, "devices")
		delete(service, "group_add")
		env, ok := service["environment"].(map[string]any)
		if !ok {
			env = map[string]any{}
			service["environment"] = env
		}
		env["SYNOPKGLAND_GPU_STATUS"] = "software"
		env["SYNOPKGLAND_GPU_DEVICE"] = ""
		if len(devices) > 0 {
			service["devices"] = devices
			// DSM's intermediate YAML emitter removes numeric string quotes.
			// Keep a nonnumeric scalar through the native rewrite, then expand the
			// discovered constant using the existing package identity environment.
			service["group_add"] = []string{"${SYNOPKGLAND_GID:+" + strconv.Itoa(gid) + "}"}
			env["SYNOPKGLAND_GPU_STATUS"] = "available"
			env["SYNOPKGLAND_GPU_DEVICE"] = strings.Split(devices[0], ":")[0]
		}
	}
	return yaml.Marshal(doc)
}

// prepareGPU reads a build-time, hash-bound template on every preinst. Missing
// hardware is software mode, never an invalid Docker mapping or an install error.
func prepareGPU(project string, inventory *DeviceInventory, expected string) error {
	if inventory == nil || inventory.GPU == nil {
		return nil
	}
	template := filepath.Join(project, "gpu.template.yaml")
	st, err := os.Lstat(template)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 4<<20 {
		return problem("COMPOSE_LOCK_INVALID", "GPU template is missing or unsafe")
	}
	raw, err := os.ReadFile(template)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != expected {
		return problem("COMPOSE_LOCK_INVALID", "GPU template digest mismatch")
	}
	nodes, gid := discoverGPU()
	raw, err = materializeGPU(raw, inventory, nodes, gid)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(project, ".gpu-compose-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(project, "docker-compose.yml"))
}
