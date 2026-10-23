package background

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const workerFreshness = 20 * time.Second

type workerObservation struct {
	WorkerInfo
	Process string `json:"process"`
}

// heartbeatRoot shares observations across CGI and worker path aliases. Never
// use TMPDIR: a service's environment must not redirect idle writes to disk.
func (s *Store) heartbeatRoot(create bool) (*os.Root, error) {
	canonical, err := filepath.EvalSymlinks(s.dir)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("synopkgland-background-%d-%s", os.Getuid(), sha256hex([]byte(canonical)))
	for _, base := range heartbeatBases() {
		if err := memoryFilesystem(base); err != nil {
			continue
		}
		path := filepath.Join(base, key)
		if create {
			err = os.Mkdir(path, 0700)
			if errors.Is(err, os.ErrPermission) {
				continue
			}
			if err != nil && !errors.Is(err, os.ErrExist) {
				return nil, err
			}
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) && !create {
			continue
		}
		if err != nil {
			return nil, err
		}
		if checkPrivateInfo(info, true) != nil || info.Mode().Perm() != 0700 {
			return nil, ErrUnsafeSpool
		}
		root, err := os.OpenRoot(path)
		if err != nil {
			return nil, err
		}
		// Verify the opened directory too; reject a replacement during open.
		f, err := root.Open(".")
		if err == nil {
			opened, statErr := f.Stat()
			f.Close()
			if statErr != nil || !os.SameFile(info, opened) {
				err = ErrUnsafeSpool
			}
		}
		if err != nil {
			root.Close()
			return nil, err
		}
		return root, nil
	}
	return nil, errors.New("package-private memory filesystem unavailable")
}

func (s *Store) readWorker() *WorkerInfo {
	root, err := s.heartbeatRoot(false)
	if err != nil {
		return nil
	}
	defer root.Close()
	f, err := root.OpenFile("worker.json", os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	if checkPrivate(f) != nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	var w workerObservation
	if err != nil || len(b) > 4096 || json.Unmarshal(b, &w) != nil || w.UID != os.Getuid() || w.Revision != s.revision {
		return nil
	}
	age := s.now().Sub(w.At)
	if age < 0 || age > workerFreshness {
		return nil
	}
	stamp, err := processStamp(w.PID)
	if err != nil || stamp != w.Process || stamp == "" {
		return nil
	}
	return &w.WorkerInfo
}

// Heartbeat publishes only the latest liveness observation in a private memory
// filesystem. Job journals and checkpoints keep their durable storage semantics.
func (s *Store) Heartbeat(ctx context.Context, info WorkerInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if info.PID != os.Getpid() || info.UID != os.Getuid() {
		return ErrInvalid
	}
	// Preserve first-use spool initialization; subsequent opens are read-only.
	spool, err := s.openRoot()
	if err != nil {
		return err
	}
	spool.Close()
	stamp, err := processStamp(info.PID)
	if err != nil {
		return err
	}
	root, err := s.heartbeatRoot(true)
	if err != nil {
		return err
	}
	defer root.Close()
	info.Revision, info.At = s.revision, s.now().UTC()
	b, err := json.Marshal(workerObservation{WorkerInfo: info, Process: stamp})
	if err != nil {
		return err
	}
	id, err := newID()
	if err != nil {
		return err
	}
	name := ".worker-" + id
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Atomic visibility without fsync: the observation intentionally dies on reboot.
	return root.Rename(name, "worker.json")
}

// ClearHeartbeat invalidates a stopped worker without touching the job spool.
// A newer process's observation is never removed by an older worker's teardown.
func (s *Store) ClearHeartbeat(info WorkerInfo) {
	root, err := s.heartbeatRoot(false)
	if err != nil {
		return
	}
	defer root.Close()
	current := s.readWorker()
	if current != nil && current.PID == info.PID && current.UID == info.UID {
		_ = root.Remove("worker.json")
	}
}
