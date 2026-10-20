package idgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// SeenAccount is a DSM account that signed in through the gateway. Local
// accounts are in /etc/passwd; the others are domain (AD/LDAP) accounts.
type SeenAccount struct {
	Name  string `json:"name"`
	Local bool   `json:"local"`
}

// localAccounts maps the uids in the passwd file to their names. A file
// without root is incomplete, not a list of deletions, and is refused.
func localAccounts(passwd string) (map[int]string, error) {
	entries, err := readPasswd(passwd)
	if err != nil {
		return nil, err
	}
	local := map[int]string{}
	for _, e := range entries {
		local[e.uid] = e.name
	}
	if local[0] != "root" {
		return nil, fmt.Errorf("%s has no root entry", passwd)
	}
	return local, nil
}

// seenStore records the uids that signed in, in a JSON file of the state
// directory, so a deletion can be noticed and acted on after the fact.
type seenStore struct{ path string }

func (s seenStore) load() (map[int]SeenAccount, error) {
	raw, err := readPrivate(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[int]SeenAccount{}, nil
	}
	if err != nil {
		return nil, err
	}
	var byUID map[string]SeenAccount
	if err := json.Unmarshal(raw, &byUID); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	seen := map[int]SeenAccount{}
	for k, a := range byUID {
		uid, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("%s: uid %q", s.path, k)
		}
		seen[uid] = a
	}
	return seen, nil
}

// record notes a sign-in; local is the current passwd file, nil when unread.
// An account seen as local stays local.
func (s seenStore) record(sub Subject, local map[int]string) error {
	return s.update(func(seen map[int]SeenAccount) bool {
		_, inPasswd := local[sub.UID]
		a := SeenAccount{Name: sub.Name, Local: inPasswd || seen[sub.UID].Local}
		if seen[sub.UID] == a {
			return false
		}
		seen[sub.UID] = a
		return true
	})
}

func (s seenStore) forget(uid int) error {
	return s.update(func(seen map[int]SeenAccount) bool {
		_, ok := seen[uid]
		delete(seen, uid)
		return ok
	})
}

// update applies change under a lock shared with other gateway processes and
// replaces the file when change reports a modification.
func (s seenStore) update(change func(map[int]SeenAccount) bool) error {
	unlock, err := lockFile(s.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	seen, err := s.load()
	if err != nil || !change(seen) {
		return err
	}
	byUID := map[string]SeenAccount{}
	for uid, a := range seen {
		byUID[strconv.Itoa(uid)] = a
	}
	raw, _ := json.Marshal(byUID)
	return replacePrivate(s.path, raw)
}

// replacePrivate atomically replaces path with a 0600 file holding data.
func replacePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
