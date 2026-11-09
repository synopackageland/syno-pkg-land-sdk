package sharedfolder

import (
	"encoding/json"
	"fmt"
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

// Subpaths reads the paired relative selections without treating missing state
// as the share root. Older installations require an explicit upgrade selection.
func Subpaths(dataDir string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		return nil, err
	}
	var state struct {
		Schema int               `json:"schema"`
		Paths  map[string]string `json:"shared_folder_paths"`
	}
	if len(raw) > 16384 || json.Unmarshal(raw, &state) != nil || state.Schema != 1 {
		return nil, fmt.Errorf("invalid folder selection record")
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
		if e != nil {
			return "", fmt.Errorf("selected folder %q: %w", name+"/"+path, e)
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("selected folder must be a directory without symlink components")
		}
	}
	return source, nil
}
