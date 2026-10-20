package compose

import (
	"context"
	"encoding/json"
	"path"
	"strings"
)

// RuntimeConfiguration replaces root-only image setup with declared private
// files and explicit upstream commands. No image/system file is modified and no
// shell expansion occurs. Files retain native ${ENV} syntax for the upstream.
type RuntimeConfiguration struct {
	Periodic    []PeriodicCommand             `json:"periodic,omitempty"`
	Environment map[string]RuntimeEnvironment `json:"environment,omitempty"`
	Files       []RuntimeFile                 `json:"files,omitempty"`
	Prepare     []PrepareCommand              `json:"prepare,omitempty"`
}

// RuntimeEnvironment reads one SDK-generated secret after entering the
// container. Values never occur in the plan, argv, lock or public SPK.
type RuntimeEnvironment struct {
	OIDCClient string `json:"oidc_client,omitempty"`
	Secret     string `json:"secret,omitempty"`
}

func (e RuntimeEnvironment) filename() string {
	if e.OIDCClient != "" {
		return "oidc-" + e.OIDCClient + ".secret"
	}
	return "runtime-" + e.Secret + ".secret"
}

type RuntimeFile struct {
	AfterPrepare bool                 `json:"after_prepare,omitempty"`
	Target       string               `json:"target"`
	Content      string               `json:"content,omitempty"`
	Source       string               `json:"source,omitempty"`
	Replace      []RuntimeReplacement `json:"replace,omitempty"`
	IfAbsent     bool                 `json:"if_absent,omitempty"`
}
type RuntimeReplacement struct {
	Old      string `json:"old"`
	New      string `json:"new"`
	Optional bool   `json:"optional,omitempty"`
}
type PrepareCommand struct {
	Argv         []string `json:"argv"`
	UnlessExists string   `json:"unless_exists,omitempty"`
	SuccessCodes []int    `json:"success_codes,omitempty"`
}

// PeriodicCommand runs fixed upstream argv without overlap. It never grants
// Docker/host access; it inherits only the same container UID and environment.
type PeriodicCommand struct {
	Argv            []string `json:"argv"`
	IntervalSeconds int      `json:"interval_seconds"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
}

// privateTarget returns a declared private directory and a direct child name.
// Keeping targets flat avoids implicit writable parents and symlink walks.
func privateTarget(dirs []WritableDirectory, target string) (string, string, bool) {
	if !containerPath(target) || strings.ContainsAny(target, "\r\n") {
		return "", "", false
	}
	for _, d := range dirs {
		dir := path.Join(d.Base, d.Path)
		if path.Dir(target) == dir {
			return dir, path.Base(target), true
		}
	}
	return "", "", false
}

func validateRuntime(i StorageInitialization) error {
	if i.Runtime == nil {
		return nil
	}
	bad := func() error {
		return problem("COMPOSE_STORAGE_INIT_INVALID", "runtime needs bounded private files and absolute prepare argv")
	}
	for _, d := range i.Directories {
		if !validWritableDirectory(d) {
			return bad()
		}
	}
	r := i.Runtime
	raw, _ := json.Marshal(i)
	if len(raw) > 48<<10 || len(r.Files) > 32 || len(r.Prepare) > 16 {
		return bad()
	}
	if len(r.Environment) > 32 {
		return bad()
	}
	if len(r.Periodic) > 8 {
		return bad()
	}
	for _, p := range r.Periodic {
		if p.IntervalSeconds < 1 || p.IntervalSeconds > 86400 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 900 || len(p.Argv) == 0 || len(p.Argv) > 64 || !containerPath(p.Argv[0]) {
			return bad()
		}
		for _, a := range p.Argv {
			if len(a) > 16<<10 || strings.ContainsRune(a, 0) {
				return bad()
			}
		}
	}
	for key, e := range r.Environment {
		id := e.Secret
		if e.OIDCClient != "" {
			id = e.OIDCClient
		}
		if !envName.MatchString(key) || (e.Secret == "") == (e.OIDCClient == "") || !runtimeSecretName.MatchString(id) || strings.HasPrefix(key, "SYNOPKGLAND_") {
			return bad()
		}
	}
	seen := map[string]bool{}
	for _, f := range r.Files {
		_, _, ok := privateTarget(i.Directories, f.Target)
		if !ok || seen[f.Target] || (f.Content == "") == (f.Source == "") || len(f.Replace) > 32 {
			return bad()
		}
		seen[f.Target] = true
		if f.Source != "" && (!containerPath(f.Source) || strings.ContainsAny(f.Source, "\r\n")) {
			return bad()
		}
		for _, v := range f.Replace {
			if v.Old == "" {
				return bad()
			}
		}
	}
	for _, c := range r.Prepare {
		if len(c.Argv) == 0 || len(c.Argv) > 64 || !containerPath(c.Argv[0]) || len(c.SuccessCodes) > 16 {
			return bad()
		}
		for _, a := range c.Argv {
			if len(a) > 16<<10 || strings.ContainsRune(a, 0) {
				return bad()
			}
		}
		if c.UnlessExists != "" {
			if _, _, ok := privateTarget(i.Directories, c.UnlessExists); !ok {
				return bad()
			}
		}
		for _, code := range c.SuccessCodes {
			if code < 0 || code > 255 {
				return bad()
			}
		}
	}
	return nil
}

// InitializeRuntime runs in the container as the package UID. Its caller owns
// the single startup deadline. A failed prepare never starts the application.
func InitializeRuntime(ctx context.Context, i StorageInitialization) error {
	if err := validateRuntime(i); err != nil {
		return err
	}
	if err := InitializeStorage(i.Directories); err != nil {
		return err
	}
	if i.Runtime == nil {
		return nil
	}
	if err := loadRuntimeEnvironment(i.Runtime.Environment); err != nil {
		return err
	}
	writeFiles := func(after bool) error {
		for n, f := range i.Runtime.Files {
			if f.AfterPrepare != after {
				continue
			}
			if err := ctx.Err(); err != nil {
				return problem("COMPOSE_RUNTIME_PREPARE_FAILED", "startup canceled")
			}
			if err := writeRuntimeFile(i.Directories, f); err != nil {
				return problem("COMPOSE_RUNTIME_CONFIG_FAILED", "file %d: %v", n, err)
			}
		}
		return nil
	}
	if err := writeFiles(false); err != nil {
		return err
	}
	for n, c := range i.Runtime.Prepare {
		if err := runPrepare(ctx, i.Directories, c); err != nil {
			return problem("COMPOSE_RUNTIME_PREPARE_FAILED", "step %d: %v", n, err)
		}
	}
	return writeFiles(true)
}
