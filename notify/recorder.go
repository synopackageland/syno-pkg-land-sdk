package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
)

const maxLogBytes = 8 << 20

// FileRecorder is a bounded, durable event log for Apps without a transactional
// outbox. Dir must be private to the package account. Apps with a database use
// Recorder in their own transaction instead. A full log refuses admission;
// retention/export is the App's responsibility and must preserve retry IDs.
type FileRecorder struct{ Dir string }

type loggedRecord struct {
	Record Record    `json:"record"`
	At     time.Time `json:"at"`
}

func (f FileRecorder) Admit(ctx context.Context, r Record, interval time.Duration) (Record, bool, error) {
	if f.Dir == "" || !deliveryID.MatchString(r.DeliveryID) || !eventID.MatchString(r.Event) || interval <= 0 {
		return Record{}, false, errors.New("invalid notification admission")
	}
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return Record{}, false, err
	}
	lock, err := os.OpenFile(filepath.Join(f.Dir, "events.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Record{}, false, err
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN && err != syscall.EINTR {
			return Record{}, false, err
		}
		select {
		case <-ctx.Done():
			return Record{}, false, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := ctx.Err(); err != nil {
		return Record{}, false, err
	}
	path := filepath.Join(f.Dir, "events.json")
	entries := map[string]loggedRecord{}
	file, err := os.Open(path)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, maxLogBytes+1))
		file.Close()
		if readErr != nil || len(data) > maxLogBytes || json.Unmarshal(data, &entries) != nil || entries == nil {
			return Record{}, false, errors.New("notification event log is unreadable")
		}
	} else if !os.IsNotExist(err) {
		return Record{}, false, err
	}
	if prior, ok := entries[r.DeliveryID]; ok {
		if !reflect.DeepEqual(prior.Record, r) {
			return Record{}, false, errors.New("delivery identity content conflict")
		}
		return prior.Record, false, nil
	}
	now := time.Now().UTC()
	var latest loggedRecord
	for _, entry := range entries {
		if entry.Record.Event == r.Event && entry.Record.Key == r.Key && !entry.At.After(now) && now.Sub(entry.At) < interval && entry.At.After(latest.At) {
			latest = entry
		}
	}
	if latest.Record.DeliveryID != "" {
		return latest.Record, false, nil
	}
	if len(entries) >= 4096 {
		return Record{}, false, errors.New("notification event log is full")
	}
	entries[r.DeliveryID] = loggedRecord{r, now}
	data, err := json.Marshal(entries)
	if err != nil || len(data) > maxLogBytes {
		return Record{}, false, errors.New("notification event log is full")
	}
	tmp, err := os.CreateTemp(f.Dir, "events.*.tmp")
	if err != nil {
		return Record{}, false, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return Record{}, false, err
	}
	dir, err := os.Open(f.Dir)
	if err != nil {
		return Record{}, false, err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return Record{}, false, err
	}
	return r, true, nil
}
