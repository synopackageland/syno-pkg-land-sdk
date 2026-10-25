package compose

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"go.yaml.in/yaml/v4"
)

const maxSourceBytes = 4 << 20
const maxSourceFiles = 128

type sourceSet struct {
	root   string
	inputs map[string]Input
	total  int
	done   map[string]walked
}

type walked struct {
	path string
	b    []byte
}

// Resolve and check every component before reading. realpath followed by an
// unchecked open would permit a symlink escape; build input must be immutable
// while planning (the CLI rechecks hashes before packaging).
func (s *sourceSet) read(name string) (string, []byte, error) {
	if name == "" || filepath.IsAbs(name) || strings.ContainsAny(name, "\\\x00") || strings.Contains(name, "://") {
		return "", nil, problem("COMPOSE_SOURCE_UNSAFE", "use a project-local regular file: %s", name)
	}
	path := filepath.Join(s.root, name)
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", nil, problem("COMPOSE_SOURCE_UNSAFE", "source escapes the project: %s", name)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	current := s.root
	for _, part := range parts {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return "", nil, problem("COMPOSE_SOURCE_UNSAFE", "source is missing or contains a symlink: %s", name)
		}
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxSourceBytes {
		return "", nil, problem("COMPOSE_SOURCE_UNSAFE", "source must be a regular file <=4 MiB: %s", name)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(b)
	if _, ok := s.inputs[rel]; !ok {
		s.total += len(b)
		if len(s.inputs) >= maxSourceFiles || s.total > 32<<20 {
			return "", nil, problem("COMPOSE_SOURCE_UNSAFE", "input closure exceeds 128 files or 32 MiB")
		}
		s.inputs[rel] = Input{Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(sum[:]), Content: b}
	}
	return path, b, nil
}

func (s *sourceSet) compose(name string, depth int) (string, []byte, error) {
	if depth > 16 {
		return "", nil, problem("COMPOSE_SOURCE_UNSAFE", "include/extends recursion exceeds 16")
	}
	// A file reached again through another include/extends edge has the same
	// closure; walking it again only multiplies work (diamond fan-out). A file
	// still being walked is not in done, so cycles keep hitting the depth cap.
	key := filepath.Clean(name)
	if d, ok := s.done[key]; ok {
		return d.path, d.b, nil
	}
	path, b, err := s.read(name)
	if err != nil {
		return "", nil, err
	}
	var doc map[string]any
	if err = yaml.Unmarshal(b, &doc); err != nil {
		return "", nil, problem("COMPOSE_MODEL_INVALID", "%s: %v", name, err)
	}
	base := filepath.Dir(name)
	local := func(value any, compose bool) error {
		n, ok := value.(string)
		if !ok || n == "" || filepath.IsAbs(n) || strings.ContainsAny(n, "$:\\") {
			return problem("COMPOSE_SOURCE_UNSAFE", "%s contains a non-local or interpolated dependency", name)
		}
		var e error
		if compose {
			_, _, e = s.compose(filepath.Join(base, n), depth+1)
		} else {
			_, _, e = s.read(filepath.Join(base, n))
		}
		return e
	}
	if includes, ok := doc["include"].([]any); ok {
		for _, entry := range includes {
			switch v := entry.(type) {
			case string:
				err = local(v, true)
				if err == nil {
					err = s.includeDotenv(filepath.Join(base, v))
				}
			case map[string]any:
				if _, ok := v["project_directory"]; ok {
					return "", nil, problem("COMPOSE_SOURCE_UNSAFE", "include.project_directory is not supported")
				}
				var paths []string
				for _, entry := range entries(v["path"]) {
					name, ok := entry.(string)
					if !ok {
						return "", nil, problem("COMPOSE_SOURCE_UNSAFE", "include path must be local")
					}
					paths = append(paths, name)
				}
				if err := sameSourceBase(paths); err != nil {
					return "", nil, err
				}
				switch p := v["path"].(type) {
				case string:
					err = local(p, true)
				case []any:
					for _, n := range p {
						if err = local(n, true); err != nil {
							break
						}
					}
				default:
					err = fmt.Errorf("invalid include path")
				}
				if err == nil && len(entries(v["env_file"])) == 0 && len(paths) > 0 {
					err = s.includeDotenv(filepath.Join(base, paths[0]))
				}
				if e, ok := v["env_file"]; ok {
					for _, n := range entries(e) {
						if err == nil {
							err = local(n, false)
						}
					}
				}
			default:
				err = fmt.Errorf("invalid include")
			}
			if err != nil {
				return "", nil, err
			}
		}
	}
	if services, ok := doc["services"].(map[string]any); ok {
		for _, raw := range services {
			service, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, ok := service["build"]; ok {
				return "", nil, problem("COMPOSE_BUILD_UNSUPPORTED", "%s: use a prebuilt image, not build", name)
			}
			if ext, ok := service["extends"].(map[string]any); ok {
				if file, ok := ext["file"]; ok {
					if err = local(file, true); err != nil {
						return "", nil, err
					}
				}
			}
			for _, key := range []string{"env_file", "label_file"} {
				for _, entry := range entries(service[key]) {
					if obj, ok := entry.(map[string]any); ok {
						entry = obj["path"]
					}
					if err = local(entry, false); err != nil {
						return "", nil, err
					}
				}
			}
		}
	}
	if s.done == nil {
		s.done = map[string]walked{}
	}
	s.done[key] = walked{path, b}
	return path, b, nil
}
func entries(v any) []any {
	if v == nil {
		return nil
	}
	if a, ok := v.([]any); ok {
		return a
	}
	return []any{v}
}
func readEnvironment(path string, b []byte, known map[string]string) (map[string]string, error) {
	values, err := dotenv.ParseWithLookup(bytes.NewReader(b), func(k string) (string, bool) { v, ok := known[k]; return v, ok })
	if err != nil {
		return nil, problem("COMPOSE_ENV_INVALID", "%s: %v", path, err)
	}
	for k := range values {
		if !envName.MatchString(k) || strings.HasPrefix(k, "COMPOSE_") || strings.HasPrefix(k, "DOCKER_") || strings.HasPrefix(k, "SYNOPKG") {
			return nil, problem("COMPOSE_ENV_INVALID", "%s cannot control loader/runtime variable %s", path, k)
		}
	}
	return values, nil
}

// SnapshotInputs inventories the same bounded local closure as Build, without
// requiring the developer to classify storage or ingress before import.
func SnapshotInputs(root string, files, envFiles []string) ([]Input, error) {
	if err := sameSourceBase(files); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 || len(files) > 32 {
		return nil, problem("COMPOSE_SOURCE_UNSAFE", "expected 1-32 Compose inputs")
	}
	s := sourceSet{root: root, inputs: map[string]Input{}}
	for _, name := range files {
		if _, _, err = s.compose(name, 0); err != nil {
			return nil, err
		}
	}
	for _, name := range envFiles {
		if _, _, err = s.read(name); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(s.inputs))
	for name := range s.inputs {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]Input, 0, len(names))
	for _, name := range names {
		out = append(out, s.inputs[name])
	}
	return out, nil
}

// The reference loader resolves ordinary overrides from the first file's base.
// Until the inventory scanner can represent a distinct base for every edge,
// reject cross-directory override sets before the loader performs any I/O.
func sameSourceBase(files []string) error {
	if len(files) == 0 {
		return nil
	}
	base := filepath.Dir(filepath.Clean(files[0]))
	for _, file := range files {
		if filepath.Dir(filepath.Clean(file)) != base {
			return problem("COMPOSE_SOURCE_UNSAFE", "override files must share a directory; cross-directory loader path bases are not supported")
		}
	}
	return nil
}
func (s *sourceSet) includeDotenv(file string) error {
	name := filepath.Join(filepath.Dir(file), ".env")
	if _, err := os.Lstat(filepath.Join(s.root, name)); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return problem("COMPOSE_SOURCE_UNSAFE", "cannot inspect include dotenv")
	}
	_, _, err := s.read(name)
	return err
}
