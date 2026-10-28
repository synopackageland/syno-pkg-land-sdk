package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxJournalBytes int64 = 10 << 20
const maxAuditBytes int64 = 50 << 20
const maxMoveBytes int64 = 16 << 30

// maxWriteBytes bounds the legacy files.write op (the App's JobFiles.Write). It
// matches delegate.MaxWriteBytes and is independent of maxReadBytes (downloads).
const maxWriteBytes int64 = 100 << 20

// errWriteTooLarge is the Writer's refusal of a staged file over maxWriteBytes;
// the handler answers it as the explicit "limit-exceeded" code.
var errWriteTooLarge = errors.New("staged file over the write limit")

// checkWriteSize refuses a staged size over maxWriteBytes.
func checkWriteSize(size int64) error {
	if size > maxWriteBytes {
		return errWriteTooLarge
	}
	return nil
}

const maxQueued = 16

type directoryIdentity struct {
	Device uint64 `json:"dev"`
	Inode  uint64 `json:"ino"`
}
type Operation struct {
	Version       int           `json:"v"`
	Package       string        `json:"package"`
	ID            string        `json:"delegation_id"`
	Job           string        `json:"job_id"`
	Subject       Subject       `json:"subject"`
	Staging       string        `json:"staging"`
	Name          string        `json:"name"`
	Size          int64         `json:"size"`
	Conflict      string        `json:"conflict,omitempty"`
	State         string        `json:"state"`
	Path          string        `json:"path,omitempty"`
	Code          string        `json:"code,omitempty"`
	Reason        string        `json:"reason,omitempty"`
	Part          *partMetadata `json:"part,omitempty"`
	PublishIntent bool          `json:"publish_intent,omitempty"`
	// CancelRequested durably keeps an acknowledged cancel of a copy in flight.
	CancelRequested string    `json:"cancel_requested,omitempty"`
	Updated         time.Time `json:"updated_at"`
	// ClientAddr is the browser address the operation is checked from.
	ClientAddr string `json:"client_addr,omitempty"`
}

// canceledBeforeAdmission marks the tombstone of a cancel that found no
// operation; admitted operations always carry their staging name.
func (op Operation) canceledBeforeAdmission() bool {
	return op.State == "canceled" && op.Staging == ""
}

type MoveReceipt struct {
	Path           string `json:"path,omitempty"`
	Name           string `json:"name,omitempty"`
	CleanupPending bool   `json:"cleanup_pending,omitempty"`
}
type AuditEvent struct {
	At        time.Time `json:"at"`
	Package   string    `json:"package"`
	Username  string    `json:"username"`
	UID       int       `json:"uid"`
	Operation string    `json:"operation"`
	Job       string    `json:"job_id,omitempty"`
	Path      string    `json:"path,omitempty"`
	Result    string    `json:"result"`
}

func recordKey(job string) string { h := sha256.Sum256([]byte(job)); return hex.EncodeToString(h[:]) }
func (s *Store) atomic(name string, b []byte) error {
	f, err := os.CreateTemp(s.Dir, ".record-")
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
	if err = os.Rename(f.Name(), filepath.Join(s.Dir, name)); err != nil {
		return err
	}
	dir, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Store) record(name string, v any) error {
	info, err := os.Lstat(filepath.Join(s.Dir, name))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > maxRequest {
		return errors.New("record mode")
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		return err
	}
	return strictJSON(b, v)
}
func (s *Store) Disabled() (bool, error) {
	var b struct {
		Disabled bool `json:"disabled"`
	}
	err := s.record("disabled.record", &b)
	if os.IsNotExist(err) {
		return false, nil
	}
	return b.Disabled, err
}
func (s *Store) SetDisabled(disabled bool) error {
	b, _ := json.Marshal(map[string]bool{"disabled": disabled})
	return s.atomic("disabled.record", b)
}
func (s *Store) PutOperation(op Operation) error {
	if op.Version != 1 || !tokenRE.MatchString(op.Job) || !idRE.MatchString(op.ID) || !validState(op.State) {
		return errors.New("operation")
	}
	b, err := json.Marshal(op)
	if err != nil || len(b) > maxRequest {
		return errors.New("operation size")
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	var total int64
	name := "operation-" + recordKey(op.Job) + ".record"
	for _, ent := range entries {
		if strings.HasPrefix(ent.Name(), "operation-") && ent.Name() != name {
			info, err := ent.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
	}
	if total+int64(len(b)) > maxJournalBytes {
		return errors.New("journal full")
	}
	return s.atomic(name, b)
}
func validState(state string) bool {
	return contains([]string{"queued", "copying", "committed", "canceled", "interrupted", "uncertain"}, state)
}
func (s *Store) GetOperation(job string) (Operation, error) {
	var op Operation
	if !tokenRE.MatchString(job) {
		return op, errors.New("job")
	}
	err := s.record("operation-"+recordKey(job)+".record", &op)
	if err != nil {
		return op, err
	}
	if op.Version != 1 || op.Job != job || !idRE.MatchString(op.ID) || !validState(op.State) {
		return op, errors.New("operation schema")
	}
	return op, nil
}
func (s *Store) Operations() ([]Operation, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	out := []Operation{}
	for _, ent := range entries {
		if !strings.HasPrefix(ent.Name(), "operation-") || !strings.HasSuffix(ent.Name(), ".record") {
			continue
		}
		var op Operation
		if err = s.record(ent.Name(), &op); err != nil {
			return nil, err
		}
		if ent.Name() != "operation-"+recordKey(op.Job)+".record" || op.Version != 1 || !validState(op.State) || !idRE.MatchString(op.ID) {
			return nil, errors.New("operation schema")
		}
		out = append(out, op)
	}
	return out, nil
}
func (s *Store) Audit(event AuditEvent) error {
	path := filepath.Join(s.Dir, "audit.jsonl")
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return errors.New("audit mode")
		}
		if info.Size()+int64(len(b)) > maxAuditBytes/2 {
			if err = os.Rename(path, filepath.Join(s.Dir, "audit.previous")); err != nil {
				return err
			}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	dir, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (e *Executor) auditLocked(s Subject, operation, job, path, result string) error {
	return e.Store.Audit(AuditEvent{At: e.Now().UTC(), Package: e.Policy.Package, Username: s.Username, UID: s.UID, Operation: operation, Job: job, Path: path, Result: result})
}

type partMetadata struct {
	Temporary   string            `json:"temporary"`
	Directory   directoryIdentity `json:"directory"`
	Part        directoryIdentity `json:"part"`
	Destination string            `json:"destination"`
}
type partRecordKey struct{}
type legacyPublishKey struct{}

type publishRejectedError struct{ Code string }

func (e publishRejectedError) Error() string { return e.Code }
