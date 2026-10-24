// Package composeimages records which locked container images this package has
// already run. It is the package lifecycle's own evidence, not Docker
// attestation: the package account cannot read the Docker image store.
package composeimages

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"
)

const FileName = "compose-images.json"

var pinned = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*(:[A-Za-z0-9_.-]+)?@sha256:[0-9a-f]{64}$`)

type record struct {
	Schema  int      `json:"schema"`
	Package string   `json:"package"`
	Images  []string `json:"images"`
}

func readPrivate(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	native, ok := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > limit || !ok || native.Uid != uint32(os.Geteuid()) || native.Nlink != 1 {
		return nil, errors.New("foreign or invalid file")
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

// Remember runs after the App passed readiness: Container Manager has then
// started containers from every image in the packaged lock.
func Remember(lockPath, dir, pkg string) error {
	raw, err := os.ReadFile(lockPath)
	if err != nil || len(raw) > 4<<20 {
		return errors.New("image lock unavailable")
	}
	var lock struct {
		Schema  int    `json:"schema"`
		Package string `json:"package"`
		Images  []struct {
			Reference string `json:"reference"`
			Pinned    bool   `json:"pinned"`
		} `json:"images"`
	}
	if json.Unmarshal(raw, &lock) != nil || lock.Schema != 1 || lock.Package != pkg || len(lock.Images) == 0 || len(lock.Images) > 64 {
		return errors.New("invalid image lock")
	}
	r := record{Schema: 1, Package: pkg}
	for _, image := range lock.Images {
		if !image.Pinned || !pinned.MatchString(image.Reference) {
			return errors.New("image is not immutable")
		}
		r.Images = append(r.Images, image.Reference)
	}
	slices.Sort(r.Images)
	r.Images = slices.Compact(r.Images)
	out, _ := json.Marshal(r)
	f, err := os.CreateTemp(dir, ".compose-images-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(out, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, FileName))
}

// Remembered returns the recorded references, or none for a missing, foreign
// or invalid record. Callers treat an empty result as "not known locally".
func Remembered(dir, pkg string) map[string]bool {
	raw, err := readPrivate(filepath.Join(dir, FileName), 64<<10)
	var r record
	if err != nil || json.Unmarshal(raw, &r) != nil || r.Schema != 1 || r.Package != pkg || len(r.Images) > 64 {
		return map[string]bool{}
	}
	result := map[string]bool{}
	for _, ref := range r.Images {
		if pinned.MatchString(ref) {
			result[ref] = true
		}
	}
	return result
}
