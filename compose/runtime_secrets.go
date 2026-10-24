package compose

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/idgateway"
	"github.com/synopackageland/syno-pkg-land-sdk/installsettings"
)

var runtimeSecretName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func prepareRuntimeSecrets(project, pkg, pkgvar string, services []Inventory) error {
	var entries []RuntimeEnvironment
	external, fallback, err := externalOriginFallback(services)
	if err != nil {
		return err
	}
	for _, s := range services {
		if s.StorageInit != nil && s.StorageInit.Runtime != nil {
			for _, e := range s.StorageInit.Runtime.Environment {
				if e.ExternalOrigin {
					continue
				}
				if e.Setting != "" {
					if _, err := readRuntimeSetting(filepath.Join(project, "../install/values", e.filename())); err != nil {
						return err
					}
					continue
				}
				entries = append(entries, e)
			}
		}
	}
	if external {
		if err := prepareExternalOrigin(project, pkg, pkgvar, fallback); err != nil {
			return err
		}
	}
	if len(entries) == 0 {
		return nil
	}
	var config struct {
		Package string `json:"package"`
		Mode    string `json:"mode"`
		OIDC    *struct {
			Clients []struct {
				ID string `json:"id"`
			} `json:"clients"`
		} `json:"oidc"`
	}
	registered := map[string]bool{}
	for _, e := range entries {
		if e.OIDCClient != "" {
			raw, err := os.ReadFile(filepath.Join(project, "../sso/gateway.json"))
			if err != nil || len(raw) > 128<<10 || json.Unmarshal(raw, &config) != nil || config.Package != pkg || config.Mode != "oidc" || config.OIDC == nil {
				return problem("COMPOSE_STORAGE_INIT_INVALID", "OIDC runtime secrets require the package gateway configuration")
			}
			for _, c := range config.OIDC.Clients {
				registered[c.ID] = true
			}
			break
		}
	}
	dir := filepath.Join(project, "secrets")
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("runtime secret directory must be private")
	}
	if owner, ok := st.Sys().(*syscall.Stat_t); !ok || owner.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("runtime secret directory must be package-owned")
	}
	for _, e := range entries {
		id := e.Secret
		if e.OIDCClient != "" {
			id = e.OIDCClient
		}
		if !runtimeSecretName.MatchString(id) || (e.Secret == "") == (e.OIDCClient == "") {
			return problem("COMPOSE_STORAGE_INIT_INVALID", "invalid runtime secret reference")
		}
		if e.OIDCClient != "" && !registered[e.OIDCClient] {
			return problem("COMPOSE_STORAGE_INIT_INVALID", "runtime OIDC client is not registered")
		}
		file := filepath.Join(dir, e.filename())
		if _, err := readRuntimeSecret(file); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		var value string
		if e.OIDCClient != "" {
			// Only read retained state here. DSM may not have created pkgvar at
			// preinst; the gateway adopts the prepared seed after install.
			b, err := readRuntimeSecret(filepath.Join(pkgvar, "sso/client-"+e.OIDCClient+".secret"))
			if err == nil {
				value = string(b)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		if value == "" {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			value = hex.EncodeToString(b)
		}
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.WriteString(value)
		if err == nil {
			err = f.Sync()
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func readRuntimeSecret(file string) ([]byte, error) {
	st, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() != 64 {
		return nil, fmt.Errorf("runtime secret must be a private 64-byte regular file")
	}
	if owner, ok := st.Sys().(*syscall.Stat_t); !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		return nil, fmt.Errorf("runtime secret must be package-owned with one link")
	}
	f, err := os.OpenFile(file, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		f.Close()
		return nil, fmt.Errorf("runtime secret changed")
	}
	b, err := io.ReadAll(io.LimitReader(f, 65))
	f.Close()
	if err != nil {
		return nil, err
	}
	if len(b) != 64 || strings.IndexFunc(string(b), func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) }) >= 0 {
		return nil, fmt.Errorf("runtime secret has invalid encoding")
	}
	return b, nil
}

func loadRuntimeEnvironment(values map[string]RuntimeEnvironment) error {
	return loadRuntimeEnvironmentAt(values, "/synopkgland")
}

func loadRuntimeEnvironmentAt(values map[string]RuntimeEnvironment, root string) error {
	for key, e := range values {
		var b []byte
		var err error
		if e.ExternalOrigin {
			b, err = readExternalOriginSnapshot(filepath.Join(root, "settings", e.filename()))
			if err == nil && len(b) == 0 {
				if err := os.Unsetenv(key); err != nil {
					return err
				}
				continue
			}
		} else if e.Setting != "" {
			b, err = readRuntimeSetting(filepath.Join(root, "settings", e.filename()))
		} else {
			b, err = readRuntimeSecret(filepath.Join(root, "secrets", e.filename()))
		}
		if err != nil {
			return problem("COMPOSE_RUNTIME_CONFIG_FAILED", "runtime secret unavailable")
		}
		if err = os.Setenv(key, string(b)); err != nil {
			return problem("COMPOSE_RUNTIME_CONFIG_FAILED", "runtime secret environment invalid")
		}
	}
	return nil
}

// The empty snapshot is deliberate: Docker still needs a regular bind source,
// but an App without a configured origin must start without this environment key.
// externalOriginFallback reports whether any key uses the shared external
// origin snapshot and the one installation setting it falls back to.
func externalOriginFallback(services []Inventory) (bool, string, error) {
	external, fallback := false, ""
	for _, s := range services {
		if s.StorageInit != nil && s.StorageInit.Runtime != nil {
			for _, e := range s.StorageInit.Runtime.Environment {
				if !e.ExternalOrigin {
					continue
				}
				if external && e.Setting != fallback {
					return false, "", problem("COMPOSE_STORAGE_INIT_INVALID", "external origin keys must share one fallback setting")
				}
				external, fallback = true, e.Setting
			}
		}
	}
	return external, fallback, nil
}

func prepareExternalOrigin(project, pkg, pkgvar, fallback string) error {
	if !filepath.IsAbs(pkgvar) || strings.ContainsAny(pkgvar, "\r\n\x00") {
		return problem("COMPOSE_RUNTIME_CONFIG_FAILED", "invalid external origin state directory")
	}
	cfg, err := idgateway.LoadConfig(filepath.Join(project, "../sso/gateway.json"))
	if err != nil || cfg.Package != pkg {
		return problem("COMPOSE_RUNTIME_CONFIG_FAILED", "external origin requires the package gateway configuration")
	}
	// pkgvar is resolved by Prepare, not by an HTTP request or container input.
	if cfg.StateDir != filepath.Join(pkgvar, "sso") && cfg.StateDir != filepath.Join("/var/packages", pkg, "var/sso") {
		return problem("COMPOSE_RUNTIME_CONFIG_FAILED", "external origin state directory differs from package state")
	}
	cfg.StateDir = filepath.Join(pkgvar, "sso")
	value := idgateway.NewExternalReader(cfg).Read().Primary
	if value == "" && fallback != "" {
		b, err := readRuntimeSetting(filepath.Join(project, "../install/values", fallback+".value"))
		if err != nil || installsettings.ValidateOrigin(string(b), 0) != nil {
			return problem("COMPOSE_RUNTIME_CONFIG_FAILED", "external origin fallback setting unavailable")
		}
		value = string(b)
	}
	dir := filepath.Join(project, "settings")
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("external origin snapshot directory must be private")
	}
	if owner, ok := st.Sys().(*syscall.Stat_t); !ok || owner.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("external origin snapshot directory must be package-owned")
	}
	f, err := os.CreateTemp(dir, ".external-origin-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(value); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, "external-origin.value")); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// RefreshExternalOrigin runs in the installed host preflight before the DSM
// worker acquires containers. Preinst preparation alone cannot apply later edits.
func RefreshExternalOrigin(project, pkgvar string) error {
	if os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		return problem("COMPOSE_ROOT_FORBIDDEN", "external origin preparation must run as the package account")
	}
	dir, err := os.Lstat(project)
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 {
		return problem("COMPOSE_SOURCE_UNSAFE", "project must be a package-owned directory")
	}
	if owner, ok := dir.Sys().(*syscall.Stat_t); !ok || owner.Uid != uint32(os.Geteuid()) {
		return problem("COMPOSE_SOURCE_UNSAFE", "project must be a package-owned directory")
	}
	f, err := os.OpenFile(filepath.Join(project, "compose.lock.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || st.Size() > 4<<20 || !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		return problem("COMPOSE_LOCK_INVALID", "invalid external origin inventory")
	}
	var lock struct {
		Package  string      `json:"package"`
		Services []Inventory `json:"services"`
	}
	decoder := json.NewDecoder(io.LimitReader(f, 4<<20))
	if decoder.Decode(&lock) != nil || !packageName.MatchString(lock.Package) || decoder.Decode(new(any)) != io.EOF {
		return problem("COMPOSE_LOCK_INVALID", "invalid external origin inventory")
	}
	external, fallback, err := externalOriginFallback(lock.Services)
	if err != nil || !external {
		return err
	}
	return prepareExternalOrigin(project, lock.Package, packageStateDirectory(lock.Package, pkgvar), fallback)
}

func readExternalOriginSnapshot(file string) ([]byte, error) {
	// Reuse the bounded private setting reader for nonempty origins. A zero-byte
	// private regular snapshot means no value, not a missing required setting.
	b, err := readRuntimeValue(file, 0)
	if err == nil && len(b) != 0 {
		err = installsettings.ValidateOrigin(string(b), 0)
	}
	return b, err
}
