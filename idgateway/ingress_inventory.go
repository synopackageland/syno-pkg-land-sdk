package idgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CheckIngressOwnership is a read-only installer preflight. DSM's web-config
// worker remains the sole registrar and removes only its own resource. No global
// registry, DSM configuration write, nginx reload or root App process is added.
func CheckIngressOwnership(pkg, prefix string) error {
	return checkIngressOwnership("/", pkg, prefix)
}

func pathsOverlap(a, b string) bool {
	a, b = strings.TrimSuffix(a, "/"), strings.TrimSuffix(b, "/")
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// Parse location declarations (including DSM's anchored regular expressions).
// Regex routes are tested against both the selected path and a child URL.
var nginxLocation = regexp.MustCompile(`(?m)\blocation\s+(?:(=|\^~|~\*?)\s+)?("[^"]*"|'[^']*'|[^\s{]+)\s*\{`)

func locationConflict(content, prefix string) bool {
	// Remove comments before inspecting literal declarations.
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if n := strings.IndexByte(line, '#'); n >= 0 {
			lines[i] = line[:n]
		}
	}
	for _, m := range nginxLocation.FindAllStringSubmatch(strings.Join(lines, "\n"), -1) {
		path := strings.Trim(m[2], `"'`)
		if strings.HasPrefix(m[1], "~") {
			// An anchored literal prefix reserves all of its subtree, even
			// when the regex matches only a particular API or asset below it.
			literal := regexp.MustCompile(`^\^(/[A-Za-z0-9_/-]+)`).FindStringSubmatch(path)
			if len(literal) > 1 {
				literalPath, selectedPath := literal[1], prefix
				if m[1] == "~*" {
					literalPath, selectedPath = strings.ToLower(literalPath), strings.ToLower(selectedPath)
				}
				if pathsOverlap(selectedPath, literalPath) {
					return true
				}
				// A disjoint full literal prefix cannot reach this ingress.
				if strings.HasSuffix(literal[1], "/") {
					continue
				}
			}
			pattern := path
			if m[1] == "~*" {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return true
			} // Unknown PCRE syntax cannot establish availability.
			if re.MatchString(strings.TrimSuffix(prefix, "/")) || re.MatchString(prefix) || re.MatchString(prefix+"index.html") {
				return true
			}
		} else if path != "/" && (pathsOverlap(prefix, path) || m[1] != "=" && strings.HasPrefix(prefix, path)) {
			return true
		}
	}
	return false
}

func checkIngressOwnership(root, pkg, prefix string) error {
	if err := validateIngressPath(pkg, prefix); err != nil {
		return err
	}
	if prefix == "" {
		return errors.New("ingress ownership requires a path")
	}
	conflict := func() error {
		return fmt.Errorf("ingress path %s is already owned by DSM or another package; select an unused path and rebuild", prefix)
	}
	unavailable := func() error {
		return errors.New("cannot verify DSM ingress ownership; restore readable route inventory and retry installation")
	}
	read := func(path string) ([]byte, error) { return os.ReadFile(filepath.Join(root, path)) }
	// DSM's native document-root entries are reserved even without active locations.
	dirs, err := os.ReadDir(filepath.Join(root, "usr/syno/synoman"))
	if err != nil {
		return unavailable()
	}
	for _, d := range dirs {
		if pathsOverlap(prefix, "/"+d.Name()+"/") {
			return conflict()
		}
	}
	// Saved aliases remain reserved independently of whether their server is running.
	aliases, err := filepath.Glob(filepath.Join(root, "usr/syno/etc/www/app.d/*.json"))
	if err != nil {
		return unavailable()
	}
	if _, err = os.ReadDir(filepath.Join(root, "usr/syno/etc/www/app.d")); err != nil {
		return unavailable()
	}
	for _, file := range aliases {
		data, err := os.ReadFile(file)
		if err != nil {
			return unavailable()
		}
		var alias struct {
			Alias string `json:"alias"`
		}
		if json.Unmarshal(data, &alias) != nil {
			return unavailable()
		}
		if alias.Alias != "" && pathsOverlap(prefix, "/"+strings.Trim(alias.Alias, "/")+"/") {
			return conflict()
		}
	}
	// Check stopped SDK gateways and package-owned portal declarations too.
	packages, err := os.ReadDir(filepath.Join(root, "var/packages"))
	if err != nil {
		return unavailable()
	}
	ownConfigs := map[string]bool{}
	for _, p := range packages {
		if p.Name() == pkg {
			for _, rel := range []string{"sso/native-nginx.conf", "daemon/ingress-nginx.conf"} {
				file := filepath.Join(root, "var/packages", pkg, "target", rel)
				if target, e := filepath.EvalSymlinks(file); e == nil {
					ownConfigs[target] = true
				}
			}
			continue
		}
		for _, rel := range []string{"sso/native-nginx.conf", "daemon/ingress-nginx.conf", "etc/portal.json"} {
			data, err := read(filepath.Join("var/packages", p.Name(), "target", rel))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return unavailable()
			}
			if strings.HasSuffix(rel, ".conf") {
				if locationConflict(string(data), prefix) {
					return conflict()
				}
			} else {
				var alias struct {
					Alias string `json:"alias"`
				}
				if json.Unmarshal(data, &alias) != nil {
					return unavailable()
				}
				if alias.Alias != "" && pathsOverlap(prefix, "/"+strings.Trim(alias.Alias, "/")+"/") {
					return conflict()
				}
			}
		}
	}
	// DSM-generated locations include custom aliases and native package routes.
	configs, err := os.ReadDir(filepath.Join(root, "usr/local/etc/nginx/conf.d"))
	if err != nil {
		return unavailable()
	}
	for _, config := range configs {
		if config.IsDir() || !strings.HasSuffix(config.Name(), ".conf") {
			continue
		}
		// http/server-wide snippets do not define paths in DSM's server.
		if !strings.HasPrefix(config.Name(), "dsm.") && !strings.HasPrefix(config.Name(), "alias.") {
			continue
		}
		file := filepath.Join(root, "usr/local/etc/nginx/conf.d", config.Name())
		resolved, err := filepath.EvalSymlinks(file)
		if err != nil {
			return unavailable()
		}
		// The worker may copy instead of symlink the package-owned file. Identify
		// ownership through DSM's generated filename, with a full package boundary.
		if ownConfigs[resolved] || strings.HasPrefix(config.Name(), "dsm.pkg-static."+pkg+"-") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return unavailable()
		}
		if locationConflict(string(data), prefix) {
			return conflict()
		}
	}
	return nil
}
