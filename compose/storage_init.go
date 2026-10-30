package compose

import (
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// WritableDirectory declares a private subtree beneath an existing container
// mount. Base is never created or chowned. Path and its parents are mode 0700
// and must belong to the current nonroot UID on every startup.
type WritableDirectory struct {
	Base string `json:"base"`
	Path string `json:"path"`
}

// StorageInitExecutable is an SDK-owned read-only mount from the SPK payload.
// Official registry images are used unchanged; no image rebuild is required.
const StorageInitExecutable = "/synopkgland/storage-init"

// StorageInitialization runs the SPK's SDK initializer as the package UID,
// before the explicit upstream argv, without a shell or container root.
type StorageInitialization struct {
	Directories []WritableDirectory   `json:"directories"`
	Runtime     *RuntimeConfiguration `json:"runtime,omitempty"`
}

func validWritableDirectory(d WritableDirectory) bool {
	return containerPath(d.Base) && path.Clean(d.Base) == d.Base && safeRelative(d.Path) && path.Clean(d.Path) == d.Path
}

// InitializeStorage creates and probes the declared private data as the current
// nonroot UID. Existing data is retained; ownership and modes are never repaired.
func InitializeStorage(directories []WritableDirectory) error {
	if len(directories) == 0 || len(directories) > 32 {
		return problem("COMPOSE_STORAGE_NOT_WRITABLE", "declare 1-32 private directories")
	}
	for _, d := range directories {
		if !validWritableDirectory(d) {
			return problem("COMPOSE_STORAGE_NOT_WRITABLE", "invalid writable directory declaration")
		}
		if err := initializeStorageDirectory(d); err != nil {
			return problem("COMPOSE_STORAGE_NOT_WRITABLE", "%s/%s: %v; use a writable mount base and a private package-UID subtree; root/chown is not used", d.Base, d.Path, err)
		}
	}
	return nil
}

func applyStorageInit(pkg string, s *types.ServiceConfig, init *StorageInitialization, rootContainer bool) error {
	if init == nil {
		return nil
	}
	invalid := func() error {
		return problem("COMPOSE_STORAGE_INIT_INVALID", "%s storage_init needs a nonroot service, 1-32 private directories on writable mounts and explicit entrypoint/command argv; /synopkgland/storage-init is reserved for the SPK initializer", s.Name)
	}
	if rootContainer || len(init.Directories) == 0 || len(init.Directories) > 32 || s.Entrypoint == nil || len(s.Entrypoint)+len(s.Command) == 0 {
		return invalid()
	}
	if err := validateRuntime(*init); err != nil {
		return err
	}
	if init.Runtime != nil {
		originalMounts := append([]types.ServiceVolumeConfig(nil), s.Volumes...)
		keys := make([]string, 0, len(init.Runtime.Environment))
		for key := range init.Runtime.Environment {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			e := init.Runtime.Environment[key]
			directory := "secrets"
			if e.Setting != "" || e.Port != "" || e.ExternalOrigin {
				directory = "settings"
			}
			target := "/synopkgland/" + directory + "/" + e.filename()
			for _, m := range originalMounts {
				if m.Target == "/synopkgland" || m.Target == "/synopkgland/secrets" || m.Target == "/synopkgland/settings" || m.Target == target {
					return invalid()
				}
			}
			// Deduplicate a secret used by multiple environment keys.
			found := false
			for _, m := range s.Volumes {
				if m.Target == target {
					found = true
				}
			}
			if !found {
				source := "/var/packages/" + pkg + "/target/compose/" + directory + "/" + e.filename()
				if e.Port != "" {
					source = "/var/packages/" + pkg + "/target/install/port-values/" + e.Port + ".value"
				}
				if e.Setting != "" && !e.ExternalOrigin {
					source = "/var/packages/" + pkg + "/target/install/values/" + e.filename()
				}
				s.Volumes = append(s.Volumes, types.ServiceVolumeConfig{Type: "bind", Source: source, Target: target, ReadOnly: true, Bind: &types.ServiceVolumeBind{CreateHostPath: false}})
			}
		}
	}
	seen := map[string]bool{}
	for _, d := range init.Directories {
		if !validWritableDirectory(d) || seen[path.Join(d.Base, d.Path)] {
			return invalid()
		}
		seen[path.Join(d.Base, d.Path)] = true
		mounted := false
		for _, m := range s.Volumes {
			if m.Target == d.Base && !m.ReadOnly {
				mounted = true
			}
			if StorageInitExecutable == m.Target || strings.HasPrefix(StorageInitExecutable, strings.TrimSuffix(m.Target, "/")+"/") || strings.HasPrefix(m.Target, StorageInitExecutable+"/") {
				return invalid()
			}
		}
		if !mounted {
			return invalid()
		}
	}
	declaration := any(init.Directories)
	flag := "--directories"
	if init.Runtime != nil {
		declaration, flag = init, "--initialization"
	}
	raw, err := json.Marshal(declaration)
	if err != nil {
		return err
	}
	if len(raw) > 48<<10 {
		return invalid()
	}
	s.Entrypoint = append([]string{StorageInitExecutable, flag, string(raw), "--"}, s.Entrypoint...)
	s.Volumes = append(s.Volumes, types.ServiceVolumeConfig{Type: "bind", Source: "/var/packages/" + pkg + "/target/compose/storage-init", Target: StorageInitExecutable, ReadOnly: true, Bind: &types.ServiceVolumeBind{CreateHostPath: false}})
	return nil
}
