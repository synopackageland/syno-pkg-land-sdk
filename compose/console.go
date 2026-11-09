package compose

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// Sudo is the explicit opt-in that lets the package-UID process of an
// interactive console service run single commands as container root with
// sudo. The service still starts as the package UID.
type Sudo struct {
	Reason string `json:"reason"`
}

// SudoCapabilities is the fixed bounding set for sudo services: sudo needs
// SETUID/SETGID (and AUDIT_WRITE for its audit record); apt-get/dpkg need
// DAC_OVERRIDE, CHOWN, FOWNER and FSETID (setgid package files). Everything
// else, including KILL, NET_*, SYS_* and MKNOD, stays dropped.
var SudoCapabilities = []string{"AUDIT_WRITE", "CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "SETGID", "SETUID"}

// Console opts one service into the SDK container init for interactive
// console Apps: an account entry for the package UID, HOME, TMPDIR and
// owner-only modes for existing credential paths. Ordinary Apps omit it.
type Console struct {
	Home string `json:"home"`
	User string `json:"user,omitempty"`
	// Tmp is TMPDIR; it defaults to the service's first scratch target.
	Tmp string `json:"tmp,omitempty"`
}

// ConsoleConfig is what the planner hands the in-container init.
type ConsoleConfig struct {
	Console
	Sudo bool `json:"sudo,omitempty"`
}

// ConsoleExecutable is the SDK binary (the SPK's storage initializer) mounted
// read-only into console services; ConsoleBin is added to their PATH.
const (
	ConsoleBin        = "/synopkgland/bin"
	ConsoleExecutable = ConsoleBin + "/synopkgland"
)

var consoleUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func planSudo(name string, policy ServicePolicy, host bool) (*Sudo, error) {
	if policy.RunAs == nil || policy.RunAs.Sudo == nil {
		return nil, nil
	}
	if policy.RunAs.Mode != "package" || len(policy.RunAs.Capabilities) > 0 {
		return nil, problem("COMPOSE_SUDO_INVALID", "service %s sudo requires run_as mode package and no added capabilities; the SDK grants a fixed set", name)
	}
	reason := strings.TrimSpace(policy.RunAs.Sudo.Reason)
	if reason == "" || len(reason) > 200 || strings.ContainsAny(reason, "\r\n\x00") {
		return nil, problem("COMPOSE_SUDO_INVALID", "service %s sudo needs a one-line reason of at most 200 characters", name)
	}
	if host {
		return nil, problem("COMPOSE_SUDO_INVALID", "service %s sudo requires bridge networking", name)
	}
	if slices.Contains(policy.Scratch, "/tmp") {
		// Scratch is a package-owned 0700 directory; apt and other system
		// accounts reached through sudo still need the image's shared /tmp.
		return nil, problem("COMPOSE_SUDO_INVALID", "service %s sudo keeps the image's own disk-backed /tmp (1777) for system tools such as apt; put scratch at another path such as /scratch (TMPDIR follows it)", name)
	}
	if policy.Console == nil {
		return nil, problem("COMPOSE_SUDO_INVALID", "service %s sudo requires console: the SDK init provides the account entry sudo needs for the package UID", name)
	}
	return &Sudo{Reason: reason}, nil
}

// writableMount reports whether p is at or below a writable mount of s.
func writableMount(s *types.ServiceConfig, p string) bool {
	below := func(target string) bool {
		target = path.Clean(target)
		return p == target || strings.HasPrefix(p, strings.TrimSuffix(target, "/")+"/")
	}
	for _, v := range s.Volumes {
		if !v.ReadOnly && below(v.Target) {
			return true
		}
	}
	for _, t := range s.Tmpfs {
		target, opts, _ := strings.Cut(t, ":")
		if !slices.Contains(strings.Split(opts, ","), "ro") && below(target) {
			return true
		}
	}
	return false
}

func applyConsole(pkg string, s *types.ServiceConfig, c *Console, scratch []string, rootContainer, sudo bool) (*Console, error) {
	if c == nil {
		return nil, nil
	}
	invalid := func(why string) (*Console, error) {
		return nil, problem("COMPOSE_CONSOLE_INVALID", "%s console %s", s.Name, why)
	}
	if rootContainer {
		return invalid("runs as the package UID; it cannot combine with container-root")
	}
	planned := *c
	if planned.User == "" {
		planned.User = strings.ToLower(pkg)
	}
	if !consoleUser.MatchString(planned.User) {
		return invalid("user must be a short lowercase account name")
	}
	if !containerPath(planned.Home) || strings.ContainsAny(planned.Home, ":\n\r") || !writableMount(s, planned.Home) {
		return invalid("home must be an absolute path at or below a writable mount of the service")
	}
	// HOME is created below its mount when missing, as the package UID.
	if t, ok := nearestTmpfs(s, path.Clean(planned.Home)); ok {
		return nil, storageOnTmpfs(s.Name, "console home "+planned.Home, t)
	}
	// TMPDIR lives on the service's disk scratch: package-owned, 0700 and
	// emptied by the SDK, never memory and never the selected NAS folder.
	if planned.Tmp == "" && len(scratch) > 0 {
		planned.Tmp = scratch[0]
	}
	if planned.Tmp != "" && (!containerPath(planned.Tmp) || strings.ContainsAny(planned.Tmp, ":\n\r") || !slices.ContainsFunc(scratch, func(t string) bool {
		return planned.Tmp == t || strings.HasPrefix(planned.Tmp, t+"/")
	})) {
		return invalid("tmp must be at or below a scratch target of the service; declare scratch for it")
	}
	if s.Entrypoint == nil || len(s.Entrypoint)+len(s.Command) == 0 {
		return invalid("needs an explicit entrypoint/command argv")
	}
	for _, v := range s.Volumes {
		t := path.Clean(v.Target)
		if t == "/synopkgland" || t == ConsoleBin || strings.HasPrefix(t, ConsoleBin+"/") {
			return invalid("cannot share " + ConsoleBin + ", which is reserved for the SDK")
		}
	}
	raw, err := json.Marshal(ConsoleConfig{Console: planned, Sudo: sudo})
	if err != nil {
		return nil, err
	}
	s.Entrypoint = append([]string{ConsoleExecutable, "container", "init", "--config", string(raw), "--"}, s.Entrypoint...)
	s.Volumes = append(s.Volumes, types.ServiceVolumeConfig{Type: "bind", Source: "/var/packages/" + pkg + "/target/compose/storage-init", Target: ConsoleExecutable, ReadOnly: true, Bind: &types.ServiceVolumeBind{CreateHostPath: false}})
	return &planned, nil
}

// SensitivePath is one entry of the credential-path table the container init
// tightens. Paths are relative to HOME; Glob entries match file names in the
// parent directory. Nothing is ever created.
type SensitivePath struct {
	Path    string
	Dir     bool
	Glob    bool
	Exclude string
}

// SensitivePaths is the single source for the documented table: directories
// become owner-only (0700), files owner-only (0600 at most).
var SensitivePaths = []SensitivePath{
	{Path: ".ssh", Dir: true},
	{Path: ".ssh/id_*", Glob: true, Exclude: ".pub"},
	{Path: ".ssh/config"},
	{Path: ".ssh/authorized_keys"},
	{Path: ".gnupg", Dir: true},
	{Path: ".netrc"},
	{Path: ".pgpass"},
	{Path: ".kube/config"},
	{Path: ".docker/config.json"},
	{Path: ".aws/credentials"},
}
