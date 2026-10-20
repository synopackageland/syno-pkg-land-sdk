package sharedfolder

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// appliedFile remembers, per key, the path the App last handed to its own
// storage, in the package's persistent directory.
const appliedFile = "shared_folders_applied.json"

// Sync is for an App whose storage registry keeps an absolute path of its
// own (a Local storage, a library root). It resolves key's current path,
// requires the declared access, and calls apply(path) when the path differs
// from the one last applied: the first use, a share moved to another volume, or
// another folder chosen for the key. A failed apply is not recorded, so the
// next call tries again. changed reports whether apply ran and succeeded.
func (c Config) Sync(key string, apply func(path string) error) (changed bool, err error) {
	path, err := c.Require(key)
	if err != nil {
		return false, err
	}
	applied := c.readApplied()
	if applied[key] == path {
		return false, nil
	}
	if err := apply(path); err != nil {
		return false, err
	}
	applied[key] = path
	return true, c.writeApplied(applied)
}

func (c Config) readApplied() map[string]string {
	applied := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(c.DataDir, appliedFile)); err == nil {
		if json.Unmarshal(data, &applied) != nil || applied == nil {
			applied = map[string]string{}
		}
	}
	return applied
}

func (c Config) writeApplied(applied map[string]string) error {
	data, err := json.Marshal(applied)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.DataDir, appliedFile+".*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), filepath.Join(c.DataDir, appliedFile)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
