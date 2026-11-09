package compose

import (
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// WritableDirectory declares a subtree beneath an existing container mount.
// Base is never created, chowned or chmodded. On package storage, Path and its
// parents are mode 0700 and must belong to the current nonroot UID on every
// startup. On a selected Shared Folder (Share, set by the planner), DSM's
// grant decides access: the subtree is created as the package UID when
// missing, and only a real write is required.
type WritableDirectory struct {
	Base  string `json:"base"`
	Path  string `json:"path"`
	Share bool   `json:"share,omitempty"`
}

// StorageInitExecutable is an SDK-owned read-only mount from the SPK payload.
// Official registry images are used unchanged; no image rebuild is required.
const StorageInitExecutable = "/synopkgland/storage-init"

// StorageInitialization runs the SPK's SDK initializer as the package UID,
// before the explicit upstream argv, without a shell or container root.
type StorageInitialization struct {
	Directories []WritableDirectory   `json:"directories,omitempty"`
	Runtime     *RuntimeConfiguration `json:"runtime,omitempty"`
	// Scratch is filled by the planner from the service's scratch targets.
	Scratch []string `json:"scratch,omitempty"`
}

func validWritableDirectory(d WritableDirectory) bool {
	return containerPath(d.Base) && path.Clean(d.Base) == d.Base && safeRelative(d.Path) && path.Clean(d.Path) == d.Path
}

// InitializeStorage creates and probes the declared data as the current
// nonroot UID. Existing data is retained; ownership and modes are never repaired.
func InitializeStorage(directories []WritableDirectory) error {
	if len(directories) > 32 {
		return problem("COMPOSE_STORAGE_NOT_WRITABLE", "declare at most 32 directories")
	}
	for _, d := range directories {
		if !validWritableDirectory(d) {
			return problem("COMPOSE_STORAGE_NOT_WRITABLE", "invalid writable directory declaration")
		}
		if err := initializeStorageDirectory(d); err != nil {
			return problem("COMPOSE_STORAGE_NOT_WRITABLE", "%s/%s: %v; use a writable mount base and a package-UID subtree; root/chown is not used", d.Base, d.Path, err)
		}
	}
	return nil
}

// ResetScratch empties each disk scratch target before the App starts. The
// directory must belong to the current nonroot UID; it is set to mode 0700.
func ResetScratch(targets []string) error {
	if len(targets) > maxScratch {
		return problem("COMPOSE_SCRATCH_NOT_READY", "declare at most %d scratch targets", maxScratch)
	}
	for _, target := range targets {
		if !containerPath(target) {
			return problem("COMPOSE_SCRATCH_NOT_READY", "invalid scratch target")
		}
		if err := resetScratchDirectory(target); err != nil {
			return problem("COMPOSE_SCRATCH_NOT_READY", "%s: %v; the SDK creates scratch as the package UID with mode 0700 at install", target, err)
		}
	}
	return nil
}

// effectiveStorageInit adds planner-owned facts to the author's declaration:
// scratch targets and whether a directory base is a selected Shared Folder.
func effectiveStorageInit(s types.ServiceConfig, policy ServicePolicy) (*StorageInitialization, error) {
	if policy.StorageInit != nil && len(policy.StorageInit.Scratch) > 0 {
		return nil, problem("COMPOSE_STORAGE_INIT_INVALID", "%s: declare scratch on the service (compose.services.%s.scratch), not in storage_init", s.Name, s.Name)
	}
	if policy.StorageInit == nil && len(policy.Scratch) == 0 {
		return nil, nil
	}
	init := &StorageInitialization{}
	if policy.StorageInit != nil {
		*init = *policy.StorageInit
		init.Directories = slices.Clone(init.Directories)
	}
	init.Scratch = slices.Clone(policy.Scratch)
	if len(init.Scratch) > 0 && (s.Entrypoint == nil || len(s.Entrypoint)+len(s.Command) == 0) {
		return nil, problem("COMPOSE_SCRATCH_INVALID", "%s scratch needs explicit entrypoint/command argv so the SDK can empty scratch before the App starts", s.Name)
	}
	for i, d := range init.Directories {
		d.Share = false
		for _, m := range s.Volumes {
			if m.Target == d.Base && strings.HasPrefix(m.Source, "${SYNOPKGLAND_MOUNT_") {
				d.Share = true
			}
		}
		init.Directories[i] = d
	}
	return init, nil
}

func applyStorageInit(pkg string, s *types.ServiceConfig, init *StorageInitialization, rootContainer bool) error {
	if init == nil {
		return nil
	}
	invalid := func() error {
		return problem("COMPOSE_STORAGE_INIT_INVALID", "%s storage_init needs explicit entrypoint/command argv and package-owned private directories, or an explicit root service with runtime environment only; /synopkgland/storage-init is reserved for the SPK initializer", s.Name)
	}
	if (rootContainer && !rootEnvironmentOnly(*init)) || (!rootContainer && len(init.Directories) == 0 && init.Runtime == nil && len(init.Scratch) == 0) || len(init.Directories) > 32 || s.Entrypoint == nil || len(s.Entrypoint)+len(s.Command) == 0 {
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
	for _, m := range s.Volumes {
		if StorageInitExecutable == m.Target || strings.HasPrefix(StorageInitExecutable, strings.TrimSuffix(m.Target, "/")+"/") || strings.HasPrefix(m.Target, StorageInitExecutable+"/") {
			return invalid()
		}
	}
	for _, d := range init.Directories {
		if t, ok := storageTmpfs(s, path.Clean(d.Base), path.Join(d.Base, d.Path)); ok && !rootContainer {
			return storageOnTmpfs(s.Name, "storage_init directory "+path.Join(d.Base, d.Path), t)
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
		}
		if !mounted {
			return invalid()
		}
	}
	declaration := any(init.Directories)
	flag := "--directories"
	if init.Runtime != nil || len(init.Scratch) > 0 {
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
