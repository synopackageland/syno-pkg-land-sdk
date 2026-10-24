// Package installsettings owns package-account installation configuration.
// No value is evaluated, logged, inferred from HTTP headers or supplied to a shell.
package installsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/externalorigin"
)

const Version = 1

type Setting struct {
	Type     string `json:"type"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}
type Config struct {
	Schema    int                `json:"schema"`
	Package   string             `json:"package"`
	HTTPSPort int                `json:"https_port,omitempty"`
	Settings  map[string]Setting `json:"settings"`
}

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
var packagePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var labelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
var invalid = errors.New("INSTALL_SETTINGS_INVALID: invalid or unavailable private installation setting")

func (c Config) Validate() error {
	if c.Schema != Version || !packagePattern.MatchString(c.Package) || c.HTTPSPort < 0 || c.HTTPSPort > 65535 || len(c.Settings) == 0 || len(c.Settings) > 32 {
		return invalid
	}
	for key, s := range c.Settings {
		if !keyPattern.MatchString(key) || (s.Type != "https-origin" && s.Type != "external-origin") || strings.TrimSpace(s.Label) == "" || len(s.Label) > 160 || strings.ContainsAny(s.Label, "\r\n\x00") {
			return invalid
		}
	}
	return nil
}

// ValidateOrigin admits a literal root HTTPS origin. A gateway listener fixes
// the authority port; no silent reverse-proxy/redirect/hostname inference.
// DNS and IPv4 are supported; IPv6/IDNs require a separate explicit contract.
func ValidateOrigin(value string, port int) error {
	if len(value) > 2048 || !strings.HasPrefix(value, "https://") || strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return invalid
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || strings.ContainsAny(strings.TrimPrefix(value, "https://"), "/?#\\") {
		return invalid
	}
	host := u.Hostname()
	if host == "" || len(host) > 253 || strings.ContainsAny(host, ":%") {
		return invalid
	}
	for _, label := range strings.Split(host, ".") {
		if !labelPattern.MatchString(label) {
			return invalid
		}
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return invalid
	}
	p := u.Port()
	if p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
			return invalid
		}
	}
	if port != 0 && p != strconv.Itoa(port) {
		return invalid
	}
	if (p == "" && u.Host != host) || (p != "" && u.Host != host+":"+p) {
		return invalid
	}
	return nil
}

// CanonicalExternalOrigin validates an external-origin installation setting.
// Unlike https-origin it requires DNS and port 443, independently of App ports.
func CanonicalExternalOrigin(value string) (string, error) {
	origin, err := externalorigin.Canonical(value)
	if err != nil {
		return "", invalid
	}
	return origin, nil
}

// ValidateExternalOrigin also rejects sharing the DSM origin. Empty settings
// are handled by the caller's Required policy, never accepted as an origin.
func ValidateExternalOrigin(value, dsmOrigin string) error {
	origin, err := CanonicalExternalOrigin(value)
	if err != nil {
		return err
	}
	dsm, err := CanonicalExternalOrigin(dsmOrigin)
	if err == nil && origin == dsm {
		return invalid
	}
	return nil
}
func settingValue(value string, setting Setting, port int) (string, error) {
	if setting.Type == "external-origin" {
		return CanonicalExternalOrigin(value)
	}
	if err := ValidateOrigin(value, port); err != nil {
		return "", err
	}
	return value, nil
}

func statePath(pkg, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if value := os.Getenv("SYNOPKG_PKGVAR"); value != "" {
		return value
	}
	if volume := os.Getenv("SYNOPKG_PKGDEST_VOL"); volume != "" {
		return filepath.Join(volume, "@appdata", pkg)
	}
	return filepath.Join("/var/packages", pkg, "var")
}
func owned(st os.FileInfo) bool {
	n, ok := st.Sys().(*syscall.Stat_t)
	return ok && n.Uid == uint32(os.Geteuid())
}
func openRoot(path string) (*os.Root, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || !owned(st) {
		return nil, invalid
	}
	return os.OpenRoot(path)
}
func privateDir(root *os.Root, path string) error {
	if err := root.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	st, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 || !owned(st) {
		return invalid
	}
	return nil
}
func readPrivate(root *os.Root, path string) ([]byte, error) {
	st, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	n, ok := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || !owned(st) || !ok || n.Nlink != 1 || st.Size() < 1 || st.Size() > 2048 {
		return nil, invalid
	}
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	now, err := f.Stat()
	if err != nil || !os.SameFile(st, now) {
		return nil, invalid
	}
	return io.ReadAll(io.LimitReader(f, 2049))
}
func writePrivate(root *os.Root, path string, b []byte) error {
	if st, err := root.Lstat(path); err == nil {
		if !st.Mode().IsRegular() || !owned(st) || st.Mode().Perm()&0077 != 0 {
			return invalid
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// An exclusive sibling plus rename ensures no partial value becomes visible.
	tmp := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return root.Rename(tmp, path)
}
func config(root *os.Root, pkg string) (Config, error) {
	var c Config
	f, err := root.OpenFile("install/settings.json", os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || c.Package != pkg || c.Validate() != nil {
		return c, invalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, invalid
	}
	return c, nil
}
func retained(pkg, pkgvar string) (*os.Root, error) {
	root, err := openRoot(statePath(pkg, pkgvar))
	if err != nil {
		return nil, err
	}
	st, err := root.Lstat("install-settings")
	if err != nil {
		root.Close()
		return nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 || !owned(st) {
		root.Close()
		return nil, invalid
	}
	dir, err := root.OpenRoot("install-settings")
	root.Close()
	return dir, err
}

// Prepare runs in preinst against incoming bytes. DSM may not have created
// pkgvar yet; only read retained state and never create the DSM-owned root.
func Prepare(payload, pkg, pkgvar string) error {
	root, err := openRoot(payload)
	if err != nil {
		return err
	}
	defer root.Close()
	c, err := config(root, pkg)
	if err != nil {
		return err
	}
	previous, err := retained(pkg, pkgvar)
	if err != nil && !os.IsNotExist(err) {
		return invalid
	}
	if previous != nil {
		defer previous.Close()
	}
	values := map[string][]byte{}
	for key, s := range c.Settings {
		value := os.Getenv("pkgwizard_setting_" + key)
		if value == "" && previous != nil {
			b, e := readPrivate(previous, key+".value")
			if e != nil && !os.IsNotExist(e) {
				return invalid
			}
			value = string(b)
		}
		if value == "" && !s.Required {
			continue
		}
		canonical, err := settingValue(value, s, c.HTTPSPort)
		if err != nil {
			return invalid
		}
		values[key] = []byte(canonical)
	}
	if err := privateDir(root, "install/values"); err != nil {
		return err
	}
	for key, b := range values {
		if err := writePrivate(root, "install/values/"+key+".value", b); err != nil {
			return err
		}
	}
	return nil
}

// Remember runs after DSM has created pkgvar; settings survive replacement of
// target and keep-data reinstalls. Each persisted value is revalidated.
func Remember(payload, pkg, pkgvar string) error {
	root, err := openRoot(payload)
	if err != nil {
		return err
	}
	defer root.Close()
	c, err := config(root, pkg)
	if err != nil {
		return err
	}
	state, err := openRoot(statePath(pkg, pkgvar))
	if err != nil {
		return err
	}
	defer state.Close()
	if err := privateDir(state, "install-settings"); err != nil {
		return err
	}
	for key, s := range c.Settings {
		b, e := readPrivate(root, "install/values/"+key+".value")
		if os.IsNotExist(e) && !s.Required {
			continue
		}
		canonical, err := settingValue(string(b), s, c.HTTPSPort)
		if e != nil || err != nil {
			return invalid
		}
		if err := writePrivate(state, "install-settings/"+key+".value", []byte(canonical)); err != nil {
			return err
		}
	}
	return nil
}

// Purge is an explicit uninstall-only operation; it cannot erase other pkgvar
// content, follow symlinks or act during an upgrade.
func Purge(pkg, pkgvar string) error {
	if !packagePattern.MatchString(pkg) {
		return invalid
	}
	if os.Getenv("SYNOPKG_PKG_STATUS") == "UPGRADE" {
		return nil
	}
	switch os.Getenv("pkgwizard_remove_additional") {
	case "true", "yes", "1", "on":
	default:
		return nil
	}
	root, err := openRoot(statePath(pkg, pkgvar))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := retained(pkg, pkgvar)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.Open(".")
	if err != nil {
		return err
	}
	names, err := entries.Readdirnames(64)
	entries.Close()
	if err != nil && err != io.EOF {
		return err
	}
	if len(names) > 32 {
		return invalid
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".value") || !keyPattern.MatchString(strings.TrimSuffix(name, ".value")) {
			return invalid
		}
		if _, err := readPrivate(dir, name); err != nil {
			return err
		}
	}
	for _, name := range names {
		if err := dir.Remove(name); err != nil {
			return err
		}
	}
	return root.Remove("install-settings")
}
