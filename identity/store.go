package identity

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type record struct {
	Subject Subject   `json:"subject"`
	Expires time.Time `json:"expires"`
}
type bucket struct {
	Start time.Time `json:"start"`
	Count int       `json:"count"`
}
type state struct {
	Sessions map[string]record `json:"sessions"`
	Rates    map[string]bucket `json:"rates"`
}

// CGI processes share a package-private, locked store. Only hashes of bearer
// IDs and rate-limit subjects are persisted; DSM credentials never enter it.
func (s *Service) transaction(fn func(*state) error) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(s.dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	st := state{Sessions: map[string]record{}, Rates: map[string]bucket{}}
	path := filepath.Join(s.dir, "state.json")
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) > 4<<20 || json.Unmarshal(raw, &st) != nil || st.Sessions == nil || st.Rates == nil {
			return errors.New("invalid identity store")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	now := s.cfg.Now()
	for k, v := range st.Sessions {
		if !now.Before(v.Expires) {
			delete(st.Sessions, k)
		}
	}
	for k, v := range st.Rates {
		if !now.Before(v.Start.Add(time.Minute)) {
			delete(st.Rates, k)
		}
	}
	result := fn(&st)
	raw, err = json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return result
}
func (s *Service) limited(st *state, ip, account string) bool {
	keys := []string{"ip:" + digest(ip), "account:" + digest(account)}
	for i, key := range keys {
		limit := 20
		if i == 1 {
			limit = 5
		}
		if st.Rates[key].Count >= limit {
			return true
		}
	}
	if len(st.Rates) > 4096 {
		return true
	}
	for _, key := range keys {
		b := st.Rates[key]
		if b.Count == 0 {
			b.Start = s.cfg.Now()
		}
		b.Count++
		st.Rates[key] = b
	}
	return false
}
