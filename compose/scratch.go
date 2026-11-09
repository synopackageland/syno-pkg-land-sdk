package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	units "github.com/docker/go-units"
)

// DefaultTmpfsBytes bounds a declared memory tmpfs that names no size. It is
// enough for sockets, PID and lock files; bulk temporary data uses scratch.
const DefaultTmpfsBytes = 64 << 20

// ScratchDirectory is the payload subdirectory that holds disk scratch.
// It lives in the package target, which exists before Container Manager
// creates the project, and is removed with the package.
const ScratchDirectory = "scratch"

const maxScratch = 8

// TmpfsMount explicitly opts a service target into a memory tmpfs. A missing
// size gets DefaultTmpfsBytes; an explicit zero (unlimited) is refused.
type TmpfsMount struct {
	Target string           `json:"target"`
	Size   *types.UnitBytes `json:"size,omitempty"`
}

// UnmarshalJSON accepts a bare target string or {target, size}.
func (t *TmpfsMount) UnmarshalJSON(raw []byte) error {
	var target string
	if json.Unmarshal(raw, &target) == nil {
		*t = TmpfsMount{Target: target}
		return nil
	}
	type plain TmpfsMount
	var v plain
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return err
	}
	*t = TmpfsMount(v)
	return nil
}

// TmpfsInventory records the bounded size that reaches Docker.
type TmpfsInventory struct {
	Target string `json:"target"`
	Bytes  int64  `json:"bytes"`
}

func scratchSource(pkg, service, target string) string {
	sum := sha256.Sum256([]byte(target))
	return "/var/packages/" + pkg + "/target/compose/" + ScratchDirectory + "/" + service + "/" + hex.EncodeToString(sum[:6])
}

func scratchHint(service, target string) string {
	return fmt.Sprintf("temporary files go to disk: declare compose.services.%s.scratch: [%s] and set TMPDIR to it if needed; a memory tmpfs needs compose.services.%s.tmpfs: [%s] (SDK default size 64 MiB, or give size)", service, target, service, target)
}

// shortTmpfs splits a Compose short tmpfs entry into target and options.
func shortTmpfs(entry string) (string, []string) {
	target, options, found := strings.Cut(entry, ":")
	if !found || options == "" {
		return target, nil
	}
	return target, strings.Split(options, ",")
}

// tmpfsPolicy validates the service's declarations and returns target -> declared size (0 = default).
func tmpfsPolicy(name string, policy ServicePolicy) (map[string]int64, map[string]bool, error) {
	declared := map[string]int64{}
	scratch := map[string]bool{}
	if len(policy.Tmpfs) > 16 || len(policy.Scratch) > maxScratch {
		return nil, nil, problem("COMPOSE_TMPFS_INVALID", "%s declares too many tmpfs or scratch targets", name)
	}
	reserved := func(target string) bool {
		return target == "/synopkgland" || strings.HasPrefix(target, "/synopkgland/")
	}
	for _, t := range policy.Tmpfs {
		if !containerPath(t.Target) || reserved(t.Target) {
			return nil, nil, problem("COMPOSE_TMPFS_INVALID", "%s tmpfs target %q must be a clean absolute container path outside /synopkgland", name, t.Target)
		}
		if _, ok := declared[t.Target]; ok {
			return nil, nil, problem("COMPOSE_TMPFS_INVALID", "%s tmpfs target %s is declared twice", name, t.Target)
		}
		size := int64(0)
		if t.Size != nil {
			if *t.Size <= 0 {
				return nil, nil, problem("COMPOSE_TMPFS_UNBOUNDED", "%s:%s tmpfs size must be positive; size 0 is unlimited memory. Omit size for the SDK default of 64 MiB", name, t.Target)
			}
			size = int64(*t.Size)
		}
		declared[t.Target] = size
	}
	for _, target := range policy.Scratch {
		if !containerPath(target) || reserved(target) {
			return nil, nil, problem("COMPOSE_SCRATCH_INVALID", "%s scratch target %q must be a clean absolute container path outside /synopkgland", name, target)
		}
		if scratch[target] {
			return nil, nil, problem("COMPOSE_SCRATCH_INVALID", "%s scratch target %s is declared twice", name, target)
		}
		if _, ok := declared[target]; ok {
			return nil, nil, problem("COMPOSE_SCRATCH_INVALID", "%s:%s cannot be both disk scratch and memory tmpfs", name, target)
		}
		scratch[target] = true
	}
	return declared, scratch, nil
}

// boundTmpfs refuses undeclared or unlimited memory mounts and gives every
// declared one an explicit size. Disk scratch replaces a Compose tmpfs at its
// target, so upstream Compose files can stay unchanged.
func boundTmpfs(s *types.ServiceConfig, policy ServicePolicy, explicitZero map[string]bool) ([]TmpfsInventory, error) {
	declared, scratch, err := tmpfsPolicy(s.Name, policy)
	if err != nil {
		return nil, err
	}
	size := func(target string, upstream int64) int64 {
		if declared[target] > 0 {
			return declared[target]
		}
		if upstream > 0 {
			return upstream
		}
		return DefaultTmpfsBytes
	}
	seen := map[string]bool{}
	var inventory []TmpfsInventory
	var short types.StringList
	for _, entry := range s.Tmpfs {
		target, options := shortTmpfs(entry)
		target = path.Clean(target)
		if scratch[target] {
			continue
		}
		if _, ok := declared[target]; !ok {
			return nil, problem("COMPOSE_TMPFS_UNDECLARED", "%s:%s is an undeclared memory tmpfs; %s", s.Name, target, scratchHint(s.Name, target))
		}
		if seen[target] {
			return nil, problem("COMPOSE_TMPFS_INVALID", "%s:%s has more than one tmpfs", s.Name, target)
		}
		seen[target] = true
		upstream := int64(0)
		kept := make([]string, 0, len(options)+1)
		for _, o := range options {
			if value, ok := strings.CutPrefix(o, "size="); ok {
				n, err := units.RAMInBytes(value)
				if err != nil || n < 0 {
					return nil, problem("COMPOSE_TMPFS_INVALID", "%s:%s has an invalid tmpfs size", s.Name, target)
				}
				if n == 0 {
					return nil, problem("COMPOSE_TMPFS_UNBOUNDED", "%s:%s tmpfs size=0 is unlimited memory; omit size for the SDK default of 64 MiB or give a positive size", s.Name, target)
				}
				upstream = n
				continue
			}
			if o != "" {
				kept = append(kept, o)
			}
		}
		bytes := size(target, upstream)
		kept = append(kept, "size="+strconv.FormatInt(bytes, 10))
		short = append(short, target+":"+strings.Join(kept, ","))
		inventory = append(inventory, TmpfsInventory{Target: target, Bytes: bytes})
	}
	s.Tmpfs = short
	volumes := s.Volumes[:0]
	for _, v := range s.Volumes {
		if v.Type != types.VolumeTypeTmpfs {
			volumes = append(volumes, v)
			continue
		}
		target := path.Clean(v.Target)
		if scratch[target] {
			continue
		}
		if _, ok := declared[target]; !ok {
			return nil, problem("COMPOSE_TMPFS_UNDECLARED", "%s:%s is an undeclared memory tmpfs; %s", s.Name, target, scratchHint(s.Name, target))
		}
		if explicitZero[target] {
			return nil, problem("COMPOSE_TMPFS_UNBOUNDED", "%s:%s tmpfs size 0 is unlimited memory; omit size for the SDK default of 64 MiB or give a positive size", s.Name, target)
		}
		if seen[target] {
			return nil, problem("COMPOSE_TMPFS_INVALID", "%s:%s has more than one tmpfs", s.Name, target)
		}
		seen[target] = true
		if v.Tmpfs == nil {
			v.Tmpfs = &types.ServiceVolumeTmpfs{}
		}
		v.Tmpfs.Size = types.UnitBytes(size(target, int64(v.Tmpfs.Size)))
		volumes = append(volumes, v)
		inventory = append(inventory, TmpfsInventory{Target: target, Bytes: int64(v.Tmpfs.Size)})
	}
	s.Volumes = volumes
	// A declared target that upstream does not mount is added by the SDK.
	for _, t := range policy.Tmpfs {
		if seen[t.Target] {
			continue
		}
		for _, v := range s.Volumes {
			if path.Clean(v.Target) == t.Target {
				return nil, problem("COMPOSE_TMPFS_INVALID", "%s:%s is already mounted; tmpfs cannot replace it", s.Name, t.Target)
			}
		}
		bytes := size(t.Target, 0)
		s.Volumes = append(s.Volumes, types.ServiceVolumeConfig{Type: types.VolumeTypeTmpfs, Target: t.Target, Tmpfs: &types.ServiceVolumeTmpfs{Size: types.UnitBytes(bytes), Mode: 01777}})
		inventory = append(inventory, TmpfsInventory{Target: t.Target, Bytes: bytes})
	}
	slices.SortFunc(inventory, func(a, b TmpfsInventory) int { return strings.Compare(a.Target, b.Target) })
	return inventory, nil
}

// addScratch binds each declared scratch target to a package-owned payload
// directory on disk. It runs after package data and Share mapping.
func addScratch(pkg string, s *types.ServiceConfig, policy ServicePolicy, rootContainer bool) error {
	if len(policy.Scratch) == 0 {
		return nil
	}
	if rootContainer {
		return problem("COMPOSE_SCRATCH_INVALID", "container-root service %s cannot bind disk scratch; keep the service nonroot or declare a bounded tmpfs", s.Name)
	}
	for _, target := range policy.Scratch {
		for _, v := range s.Volumes {
			if path.Clean(v.Target) == target {
				return problem("COMPOSE_SCRATCH_INVALID", "%s:%s is already mounted; scratch replaces only a Compose tmpfs", s.Name, target)
			}
		}
		s.Volumes = append(s.Volumes, types.ServiceVolumeConfig{Type: "bind", Source: scratchSource(pkg, s.Name, target), Target: target, Bind: &types.ServiceVolumeBind{CreateHostPath: false}})
	}
	return nil
}

// explicitZeroTmpfs finds long-syntax tmpfs volumes whose size is written as
// zero. The typed model cannot distinguish that from an omitted size.
func explicitZeroTmpfs(model map[string]any) map[string]map[string]bool {
	found := map[string]map[string]bool{}
	services, _ := model["services"].(map[string]any)
	for name, raw := range services {
		service, _ := raw.(map[string]any)
		volumes, _ := service["volumes"].([]any)
		for _, rawVolume := range volumes {
			v, _ := rawVolume.(map[string]any)
			if v == nil || v["type"] != "tmpfs" {
				continue
			}
			options, _ := v["tmpfs"].(map[string]any)
			value, ok := options["size"]
			if !ok {
				continue
			}
			zero := false
			switch n := value.(type) {
			case int:
				zero = n <= 0
			case int64:
				zero = n <= 0
			case float64:
				zero = n <= 0
			case string:
				b, err := units.RAMInBytes(n)
				zero = err != nil || b <= 0
			}
			target, _ := v["target"].(string)
			if zero && target != "" {
				if found[name] == nil {
					found[name] = map[string]bool{}
				}
				found[name][path.Clean(target)] = true
			}
		}
	}
	return found
}

// tmpfsTargets lists every memory tmpfs that reaches Docker for s.
func tmpfsTargets(s *types.ServiceConfig) []string {
	var targets []string
	for _, entry := range s.Tmpfs {
		target, _ := shortTmpfs(entry)
		targets = append(targets, path.Clean(target))
	}
	for _, v := range s.Volumes {
		if v.Type == types.VolumeTypeTmpfs {
			targets = append(targets, path.Clean(v.Target))
		}
	}
	return targets
}

// storageTmpfs returns the tmpfs a nonroot service would create p on: a
// tmpfs at base, or one between base (exclusive) and p (inclusive). DSM's
// Docker recreates a tmpfs as root 0755 on `docker restart` and ignores the
// declared mode (and uid), so the package UID could create p on the first
// start but not after a restart.
func storageTmpfs(s *types.ServiceConfig, base, p string) (string, bool) {
	below := func(child, parent string) bool {
		return strings.HasPrefix(child, strings.TrimSuffix(parent, "/")+"/")
	}
	for _, t := range tmpfsTargets(s) {
		if t == base || below(t, base) && (p == t || below(p, t)) {
			return t, true
		}
	}
	return "", false
}

func storageOnTmpfs(service, what, target string) error {
	return problem("COMPOSE_STORAGE_ON_TMPFS", "%s %s is on the memory tmpfs %s; DSM's Docker recreates a tmpfs as root 0755 on restart and ignores its mode, so the package UID cannot create it after a restart. Use disk scratch (compose.services.%s.scratch: [%s]) for temporary files, or a named volume or package data for files that must persist", service, what, target, service, target)
}

// nearestTmpfs reports the tmpfs when the closest mount at or above p is one.
func nearestTmpfs(s *types.ServiceConfig, p string) (string, bool) {
	best, tmpfs := "", false
	consider := func(target string, isTmpfs bool) {
		target = path.Clean(target)
		if (p == target || strings.HasPrefix(p, strings.TrimSuffix(target, "/")+"/")) && len(target) > len(best) {
			best, tmpfs = target, isTmpfs
		}
	}
	for _, v := range s.Volumes {
		consider(v.Target, v.Type == types.VolumeTypeTmpfs)
	}
	for _, entry := range s.Tmpfs {
		target, _ := shortTmpfs(entry)
		consider(target, true)
	}
	return best, tmpfs
}
