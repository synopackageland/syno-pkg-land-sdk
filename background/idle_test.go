package background

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Include directories and inode identity: replacing equal bytes is still a write.
func diskSnapshot(t *testing.T, dir string) map[string]any {
	t.Helper()
	out := map[string]any{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		out[path] = []any{info.ModTime(), info.Size(), info.Mode()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIdleTicksNeverWritePersistentSpool(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	info := WorkerInfo{PID: os.Getpid(), UID: os.Getuid(), Username: "pkg"}
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	before := diskSnapshot(t, dir)
	for range 12 {
		if err := s.Heartbeat(ctx, info); err != nil {
			t.Fatal(err)
		}
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		if job, err := s.Claim(ctx); err != nil || job != nil {
			t.Fatalf("claim: %v %v", job, err)
		}
		st, err := s.Status(ctx)
		if err != nil || st.Worker == nil {
			t.Fatalf("status: %+v %v", st, err)
		}
	}
	if after := diskSnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("idle mutated durable spool:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestHeartbeatRejectsStaleAndFutureObservations(t *testing.T) {
	for _, delta := range []time.Duration{time.Minute, -time.Minute} {
		t.Run(delta.String(), func(t *testing.T) {
			s, _ := newStore(t)
			ctx := context.Background()
			if err := s.Heartbeat(ctx, WorkerInfo{PID: os.Getpid(), UID: os.Getuid()}); err != nil {
				t.Fatal(err)
			}
			now := s.now()
			s.now = func() time.Time { return now.Add(delta) }
			st, err := s.Status(ctx)
			if err != nil || st.Worker != nil {
				t.Fatalf("stale observation accepted: %+v %v", st, err)
			}
		})
	}
}

func TestHeartbeatCanonicalSpoolAlias(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(dir), alias); err != nil {
		t.Fatal(err)
	}
	other := NewStore(filepath.Join(alias, filepath.Base(dir)))
	if err := s.Heartbeat(ctx, WorkerInfo{PID: os.Getpid(), UID: os.Getuid()}); err != nil {
		t.Fatal(err)
	}
	st, err := other.Status(ctx)
	if err != nil || st.Worker == nil || st.Worker.PID != os.Getpid() {
		t.Fatalf("alias missed worker: %+v %v", st, err)
	}
}

func TestWorkerObservationRejectsChangedProcessRevisionAndUnsafeFiles(t *testing.T) {
	for _, mode := range []string{"process", "revision", "uid", "symlink", "wide", "clear"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newStore(t)
			ctx := context.Background()
			if err := s.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			info := WorkerInfo{PID: os.Getpid(), UID: os.Getuid()}
			if err := s.Heartbeat(ctx, info); err != nil {
				t.Fatal(err)
			}
			root, err := s.heartbeatRoot(false)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			switch mode {
			case "clear":
				s.ClearHeartbeat(info)
			case "symlink":
				if err := root.Remove("worker.json"); err != nil {
					t.Fatal(err)
				}
				if err := root.Symlink("/etc/passwd", "worker.json"); err != nil {
					t.Fatal(err)
				}
			case "wide":
				if err := root.Chmod("worker.json", 0644); err != nil {
					t.Fatal(err)
				}
			default:
				data, err := root.ReadFile("worker.json")
				if err != nil {
					t.Fatal(err)
				}
				var w workerObservation
				if err := json.Unmarshal(data, &w); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "process":
					w.Process += "old-boot-or-reused-pid"
				case "revision":
					w.Revision++
				case "uid":
					w.UID++
				}
				data, _ = json.Marshal(w)
				f, err := root.OpenFile("worker.json", os.O_WRONLY|os.O_TRUNC, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.Write(data)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			st, err := s.Status(ctx)
			if err != nil || st.Worker != nil {
				t.Fatalf("invalid observation accepted: %+v %v", st, err)
			}
		})
	}
}
