package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Dir and its ancestors are validated root-owned at service startup. No client
// supplies a store path. Atomic replacement + fsync persists revocations.
type Store struct{ Dir string }

func (s *Store) Put(d Delegation) error {
	if !idRE.MatchString(d.ID) {
		return errors.New("id")
	}
	b, err := json.Marshal(d)
	if err != nil || len(b) > maxRequest {
		return errors.New("grant size")
	}
	f, err := os.CreateTemp(s.Dir, ".delegation-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(s.Dir, d.ID+".json")); err != nil {
		return err
	}
	dir, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Store) Get(id string) (Delegation, error) {
	var d Delegation
	if !idRE.MatchString(id) {
		return d, errors.New("id")
	}
	path := filepath.Join(s.Dir, id+".json")
	info, err := os.Lstat(path)
	if err != nil {
		return d, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > maxRequest {
		return d, errors.New("store mode")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return d, err
	}
	err = strictJSON(b, &d)
	if d.ID != id {
		return d, errors.New("id mismatch")
	}
	return d, err
}
func (s *Store) List() ([]Delegation, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	ds := []Delegation{}
	for _, ent := range entries {
		if filepath.Ext(ent.Name()) != ".json" || !idRE.MatchString(strings.TrimSuffix(ent.Name(), ".json")) {
			continue
		}
		d, err := s.Get(ent.Name()[:len(ent.Name())-5])
		if err != nil {
			return nil, err
		}
		ds = append(ds, d)
	}
	return ds, nil
}
