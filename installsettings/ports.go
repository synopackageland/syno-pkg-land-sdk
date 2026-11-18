package installsettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Port struct {
	Default  int    `json:"default"`
	Protocol string `json:"protocol"`
	// Conflict is "" (a confirmed service conflict refuses the port) or
	// ConflictWarn (it is reported and the port is kept).
	Conflict string `json:"conflict,omitempty"`
}

// ConflictWarn keeps a port that DSM's service registry reports in use and
// reports a warning instead. Format, range, reserved and duplicate checks
// still refuse it; DSM's resource acquisition and binding stay the final check.
const ConflictWarn = "warn"

type PortConfig struct {
	Schema  int               `json:"schema"`
	Package string            `json:"package"`
	Ports   map[string]Port   `json:"ports"`
	Files   map[string]string `json:"files"`
}

var ErrPort = errors.New("INSTALL_PORT_INVALID: choose an available non-reserved port between 1024 and 65535")

func portServiceID(pkg, key string) string {
	return "synopkgland." + strings.ReplaceAll(pkg, "_", "-") + "." + strings.ReplaceAll(key, "_", "-")
}

func ValidPort(n int) bool {
	if n < 1024 || n > 65535 {
		return false
	}
	switch n {
	case 1900, 5000, 5001, 5005, 5006, 5353, 6690:
		return false
	}
	return true
}
func portPath(path string) bool {
	switch path {
	case "sso/gateway.json", "sso/native-nginx.conf", "ui/config", "daemon/config.json", "compose/request.json", "install/settings.json":
		return true
	}
	return regexp.MustCompile(`^ui/[A-Za-z0-9_.-]+\.js$`).MatchString(path) && !strings.Contains(path, "..")
}
func portConfig(root *os.Root, pkg string) (PortConfig, error) {
	var cfg PortConfig
	f, err := root.Open("install/ports.json")
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return cfg, ErrPort
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&cfg) != nil || cfg.Schema != 1 || cfg.Package != pkg || !packagePattern.MatchString(pkg) || len(cfg.Ports) == 0 || len(cfg.Ports) > 16 || len(cfg.Files) > 16 {
		return cfg, ErrPort
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return cfg, ErrPort
	}
	for key, p := range cfg.Ports {
		if !keyPattern.MatchString(key) || !ValidPort(p.Default) || (p.Protocol != "tcp" && p.Protocol != "udp") || (p.Conflict != "" && p.Conflict != ConflictWarn) {
			return cfg, ErrPort
		}
	}
	for path, template := range cfg.Files {
		if !portPath(path) || len(template) > 1<<20 {
			return cfg, ErrPort
		}
	}
	return cfg, nil
}
func portState(pkg, pkgvar string) (*os.Root, error) {
	root, err := openRoot(statePath(pkg, pkgvar))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	st, err := root.Lstat("install-ports")
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 || !owned(st) {
		return nil, ErrPort
	}
	return root.OpenRoot("install-ports")
}
func portValue(raw string) (int, error) {
	n, e := strconv.Atoi(raw)
	if e != nil || strconv.Itoa(n) != raw || !ValidPort(n) {
		return 0, ErrPort
	}
	return n, nil
}

func writePortFile(root *os.Root, path string, content []byte) error {
	st, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || !owned(st) {
		return ErrPort
	}
	tmp := path + ".ports.tmp"
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err = f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, path)
}

// PreparePorts materializes only generated, bounded payload files. It does not
// touch DSM configuration, bind sockets, call a shell or create pkgvar.
// A warn-policy conflict is reported on stderr, which DSM keeps in the
// package's installation log.
func PreparePorts(payload, pkg, pkgvar string) error {
	return preparePortsWarn(payload, pkg, pkgvar, CheckPort, func(w string) { fmt.Fprintln(os.Stderr, w) })
}

type portCheck func(context.Context, string, string, Port, int) error

func preparePorts(payload, pkg, pkgvar string, check portCheck) error {
	return preparePortsWarn(payload, pkg, pkgvar, check, func(string) {})
}

// conflictWarning applies a port's conflict policy to a check result.
func conflictWarning(key string, p Port, n int, err error) (string, error) {
	if err == nil {
		return "", nil
	}
	if p.Conflict == ConflictWarn {
		return fmt.Sprintf("INSTALL_PORT_CONFLICT_WARNING: port %d (%s) may already be in use; it was kept: %v", n, key, err), nil
	}
	return "", fmt.Errorf("%w (%s)", err, key)
}

func preparePortsWarn(payload, pkg, pkgvar string, check portCheck, warn func(string)) error {
	if os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		return ErrPort
	}
	root, err := openRoot(payload)
	if err != nil {
		return err
	}
	defer root.Close()
	cfg, err := portConfig(root, pkg)
	if err != nil {
		return err
	}
	previous, err := portState(pkg, pkgvar)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if previous != nil {
		defer previous.Close()
	}
	values := map[string]int{}
	used := map[string]bool{}
	keys := make([]string, 0, len(cfg.Ports))
	for key := range cfg.Ports {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p := cfg.Ports[key]
		raw := os.Getenv("pkgwizard_port_" + key)
		// An upgrade always retains installed values; ChangePorts changes them.
		if os.Getenv("SYNOPKG_PKG_STATUS") == "UPGRADE" {
			raw = ""
		}
		if raw == "" && previous != nil {
			b, e := readPrivate(previous, key+".value")
			if e != nil && !os.IsNotExist(e) {
				return e
			}
			raw = string(b)
		}
		if raw == "" {
			raw = strconv.Itoa(p.Default)
		}
		n, e := portValue(raw)
		if e != nil {
			return e
		}
		id := p.Protocol + ":" + raw
		if used[id] {
			return ErrPort
		}
		used[id] = true
		warning, e := conflictWarning(key, p, n, check(context.Background(), pkg, key, p, n))
		if e != nil {
			return e
		}
		if warning != "" {
			warn(warning)
		}
		values[key] = n
	}
	return materializePorts(root, cfg, pkg, keys, values)
}

// materializePorts renders the generated files, selected values, DSM service
// definition and environment from the package's own port templates.
func materializePorts(root *os.Root, cfg PortConfig, pkg string, keys []string, values map[string]int) error {
	var err error
	outputs := map[string][]byte{}
	for path, template := range cfg.Files {
		for key, n := range values {
			template = strings.ReplaceAll(template, "@@PORT_"+key+"@@", strconv.Itoa(n))
		}
		if strings.Contains(template, "@@PORT_") {
			return ErrPort
		}
		outputs[path] = []byte(template)
	}
	for path, content := range outputs {
		if err = writePortFile(root, path, content); err != nil {
			return err
		}
	}
	if err = privateDir(root, "install/port-values"); err != nil {
		return err
	}
	var service, environment strings.Builder
	for _, key := range keys {
		n := values[key]
		p := cfg.Ports[key]
		fmt.Fprintf(&service, "[%s]\ntitle=\"%s\"\ndesc=\"%s\"\nport_forward=\"no\"\ndst.ports=\"%d/%s\"\n\n", portServiceID(pkg, key), pkg, pkg, n, p.Protocol)
		fmt.Fprintf(&environment, "SYNOPKGLAND_PORT_%s=%d\n", strings.ToUpper(key), n)
		if err = writePrivate(root, "install/port-values/"+key+".value", []byte(strconv.Itoa(n))); err != nil {
			return err
		}
	}
	if err = writePortFile(root, "install/"+pkg+".sc", []byte(service.String())); err != nil {
		return err
	}
	return writePrivate(root, "install/ports.env", []byte(environment.String()))
}

// ChangePorts changes installed ports after installation, for an App's own
// settings page. Run it as the package account. Only declared keys change;
// format, range, reserved and duplicate values are refused and a conflict
// follows each port's policy (warnings are returned). It rewrites the
// generated payload files and the remembered values, so upgrades keep the
// change. DSM applies the new nginx and service resources at the next package
// start: restart the package afterwards (see selfrestart).
func ChangePorts(payload, pkg, pkgvar string, changes map[string]int) ([]string, error) {
	return changePorts(payload, pkg, pkgvar, changes, CheckPort)
}

func changePorts(payload, pkg, pkgvar string, changes map[string]int, check portCheck) ([]string, error) {
	if os.Getuid() == 0 || os.Getuid() != os.Geteuid() || len(changes) == 0 {
		return nil, ErrPort
	}
	root, err := openRoot(payload)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	cfg, err := portConfig(root, pkg)
	if err != nil {
		return nil, err
	}
	values := map[string]int{}
	keys := make([]string, 0, len(cfg.Ports))
	for key := range cfg.Ports {
		b, e := readPrivate(root, "install/port-values/"+key+".value")
		if e != nil {
			return nil, e
		}
		if values[key], e = portValue(string(b)); e != nil {
			return nil, e
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for key, n := range changes {
		if _, ok := cfg.Ports[key]; !ok || !ValidPort(n) {
			return nil, fmt.Errorf("%w (%s)", ErrPort, key)
		}
		values[key] = n
	}
	used := map[string]bool{}
	for _, key := range keys {
		id := cfg.Ports[key].Protocol + ":" + strconv.Itoa(values[key])
		if used[id] {
			return nil, fmt.Errorf("%w (%s)", ErrPort, key)
		}
		used[id] = true
	}
	var warnings []string
	for _, key := range keys {
		n, changed := changes[key]
		if !changed {
			continue
		}
		warning, e := conflictWarning(key, cfg.Ports[key], n, check(context.Background(), pkg, key, cfg.Ports[key], n))
		if e != nil {
			return nil, e
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	if err = materializePorts(root, cfg, pkg, keys, values); err != nil {
		return nil, err
	}
	state, err := openRoot(statePath(pkg, pkgvar))
	if err != nil {
		return nil, err
	}
	defer state.Close()
	if err = privateDir(state, "install-ports"); err != nil {
		return nil, err
	}
	for _, key := range keys {
		if err = writePrivate(state, "install-ports/"+key+".value", []byte(strconv.Itoa(values[key]))); err != nil {
			return nil, err
		}
	}
	return warnings, nil
}

func RememberPorts(payload, pkg, pkgvar string) error {
	root, err := openRoot(payload)
	if err != nil {
		return err
	}
	defer root.Close()
	cfg, err := portConfig(root, pkg)
	if err != nil {
		return err
	}
	state, err := openRoot(statePath(pkg, pkgvar))
	if err != nil {
		return err
	}
	defer state.Close()
	if err = privateDir(state, "install-ports"); err != nil {
		return err
	}
	for key := range cfg.Ports {
		b, e := readPrivate(root, "install/port-values/"+key+".value")
		if e != nil {
			return e
		}
		if _, e = portValue(string(b)); e != nil {
			return e
		}
		if e = writePrivate(state, "install-ports/"+key+".value", b); e != nil {
			return e
		}
	}
	return nil
}

// PortValues returns the values materialized by the installer, never caller input.
func PortValues(payload string) (map[string]int, error) {
	root, err := openRoot(payload)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open("install/ports.json")
	if os.IsNotExist(err) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if len(raw) > 4<<20 {
		return nil, ErrPort
	}
	if os.IsNotExist(err) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	var header struct {
		Package string `json:"package"`
	}
	if json.Unmarshal(raw, &header) != nil {
		return nil, ErrPort
	}
	cfg, err := portConfig(root, header.Package)
	if err != nil {
		return nil, err
	}
	values := map[string]int{}
	for key := range cfg.Ports {
		b, e := readPrivate(root, filepath.Join("install/port-values", key+".value"))
		if e != nil {
			return nil, e
		}
		n, e := portValue(string(b))
		if e != nil {
			return nil, e
		}
		values[key] = n
	}
	return values, nil
}
