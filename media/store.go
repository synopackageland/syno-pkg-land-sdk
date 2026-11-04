package media

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const maxGrants = 4096
const maxStoreBytes = 8 << 20

type binding struct {
	User       string `json:"user"`
	UID        string `json:"uid"`
	Entry      string `json:"entry"`
	Generation string `json:"generation"`
	Session    string `json:"session"`
}
type record struct {
	Binding  binding   `json:"binding"`
	Asset    string    `json:"asset"`
	Revision string    `json:"revision"`
	Methods  []string  `json:"methods"`
	Expires  time.Time `json:"expires"`
}

func randomID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// transaction serializes CGI and daemon access. Revocation cannot be undone
// by a renewal using a stale snapshot. No DSM credential enters this store.
func (s *Service) transaction(fn func(map[string]record) error) error {
	lock, err := os.OpenFile(filepath.Join(s.dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	records := map[string]record{}
	file := filepath.Join(s.dir, "grants.json")
	f, err := os.Open(file)
	if err == nil {
		raw, e := io.ReadAll(io.LimitReader(f, maxStoreBytes+1))
		f.Close()
		if e != nil || len(raw) > maxStoreBytes || json.Unmarshal(raw, &records) != nil || records == nil {
			return errors.New("invalid media store")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for key, rec := range records {
		if !s.now().Before(rec.Expires) {
			delete(records, key)
		}
	}
	result := fn(records)
	raw, err := json.Marshal(records)
	if err != nil {
		return err
	}
	if len(raw) > maxStoreBytes {
		return errors.New("media store full")
	}
	tmp, err := os.CreateTemp(s.dir, ".grants-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), file); err != nil {
		return err
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return err
	}
	return result
}

// Revoke invalidates an id across all processes. It is safe to repeat.
func (s *Service) Revoke(id string) error {
	if !validID(id) {
		return apiBadID()
	}
	return s.transaction(func(records map[string]record) error { delete(records, digest(id)); return nil })
}

// loadKey publishes a complete random key atomically, including when two CGI
// processes initialize the service together. Never rotate this on each request.
func loadKey(dir string) ([]byte, error) {
	name := filepath.Join(dir, "seal.key")
	read := func() ([]byte, error) {
		st, err := os.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
			return nil, errors.New("invalid media key permissions")
		}
		b, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		if len(b) != 32 {
			return nil, errors.New("invalid media key")
		}
		return b, nil
	}
	if b, err := read(); err == nil {
		return b, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".key-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(key); err != nil {
		tmp.Close()
		return nil, err
	}
	if err = tmp.Close(); err != nil {
		return nil, err
	}
	if err = os.Link(tmp.Name(), name); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return read()
}
