package sharedfolder

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ValidSubpath accepts an existing folder relative to one shared folder.
// Empty selects its root; paths are never cleaned into a different selection.
func ValidSubpath(path string) bool {
	if len(path) > 1024 || strings.ContainsAny(path, "\\\"\x00") {
		return false
	}
	for _, c := range path {
		if c < 32 || c == 127 {
			return false
		}
	}
	if path == "" {
		return true
	}
	for _, p := range strings.Split(path, "/") {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return true
}

// Subpaths reads paired relative selections. Published share-only records
// represent the share root; missing files and malformed paired records fail.
func Subpaths(dataDir string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		return nil, err
	}
	var state struct {
		Schema   int               `json:"schema"`
		Folders  map[string]string `json:"shared_folders"`
		RawPaths json.RawMessage   `json:"shared_folder_paths"`
		Paths    map[string]string `json:"-"`
	}
	if len(raw) > 16384 || json.Unmarshal(raw, &state) != nil || state.Schema != 1 {
		return nil, fmt.Errorf("invalid folder selection record")
	}
	if state.RawPaths == nil {
		// Published share-only records select the share root. Only absence is
		// legacy: explicit null, corrupt paths and partial pairs remain invalid.
		state.Paths = make(map[string]string, len(state.Folders))
		for key, name := range state.Folders {
			if !validName(name) {
				return nil, fmt.Errorf("invalid shared folder selection")
			}
			state.Paths[key] = ""
		}
	} else if json.Unmarshal(state.RawPaths, &state.Paths) != nil || state.Paths == nil {
		return nil, fmt.Errorf("invalid folder selection record")
	}
	for key := range state.Folders {
		if _, ok := state.Paths[key]; !ok {
			return nil, fmt.Errorf("incomplete folder selection record")
		}
	}

	for _, p := range state.Paths {
		if !ValidSubpath(p) {
			return nil, fmt.Errorf("invalid relative folder selection")
		}
	}
	return state.Paths, nil
}

// ResolveSubpath resolves a share afresh and rejects every symlink component.
// It neither creates directories nor changes permissions.
func ResolveSubpath(root, name, path string) (string, error) {
	return resolveSubpath(root, name, path, false)
}

// ResolveSubpathForInstall checks structure before DSM grants access. An
// inaccessible descendant is deferred, never misreported as missing.
func ResolveSubpathForInstall(root, name, path string) (string, error) {
	return resolveSubpath(root, name, path, true)
}

func resolveSubpath(root, name, path string, deferPermission bool) (string, error) {
	if !ValidSubpath(path) {
		return "", fmt.Errorf("invalid relative folder path")
	}
	base, err := Resolve(root, name)
	if err != nil {
		return "", err
	}
	source := filepath.Join(base, path)
	for current := source; current != filepath.Clean(root) && current != "/" && current != "."; current = filepath.Dir(current) {
		st, e := os.Lstat(current)
		if deferPermission && errors.Is(e, fs.ErrPermission) {
			continue
		}
		if e != nil {
			return "", fmt.Errorf("selected folder %q: %w", name+"/"+path, e)
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("selected folder must be a directory without symlink components")
		}
	}
	if root == "/" {
		if err := validateSelectedMount(source); err != nil {
			return "", err
		}
	}
	return source, nil
}
