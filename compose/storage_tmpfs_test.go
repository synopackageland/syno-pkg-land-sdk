package compose

import (
	"context"
	"strings"
	"testing"
)

// DSM's Docker recreates a tmpfs as root 0755 on restart and ignores its
// declared mode, so a nonroot service must not create directories there.
func TestStorageOnTmpfsIsRefusedForNonrootServices(t *testing.T) {
	for name, c := range map[string]struct {
		service string
		policy  ServicePolicy
	}{
		"declared-base": {"", ServicePolicy{Tmpfs: []TmpfsMount{{Target: "/run/app"}},
			StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/run/app", Path: "runtime"}}}}},
		"compose-short-base": {"    tmpfs: ['/run/app:mode=1777']\n", ServicePolicy{Tmpfs: []TmpfsMount{{Target: "/run/app"}},
			StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/run/app", Path: "runtime"}}}}},
		"compose-long-base": {"    volumes: [{type: tmpfs, target: /run/app, tmpfs: {mode: 0o1777}}]\n", ServicePolicy{Tmpfs: []TmpfsMount{{Target: "/run/app"}},
			StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/run/app", Path: "runtime"}}}}},
		"tmpfs-below-base": {"    volumes: ['state:/state']\n", ServicePolicy{Tmpfs: []TmpfsMount{{Target: "/state/run"}},
			StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/state", Path: "run/sockets"}}}}},
		"tmpfs-at-directory": {"    volumes: ['state:/state']\n", ServicePolicy{Tmpfs: []TmpfsMount{{Target: "/state/run"}},
			StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/state", Path: "run"}}}}},
		"console-home": {"", ServicePolicy{Tmpfs: []TmpfsMount{{Target: "/home"}}, Console: &Console{Home: "/home/app"}}},
	} {
		t.Run(name, func(t *testing.T) {
			r := tmpfsRequest(t, c.service)
			r.Services["openlist"] = c.policy
			_, err := Build(context.Background(), r)
			if err == nil || !strings.Contains(err.Error(), "COMPOSE_STORAGE_ON_TMPFS") || !strings.Contains(err.Error(), "scratch") || !strings.Contains(err.Error(), "0755") {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

func TestStorageBesideTmpfsIsAccepted(t *testing.T) {
	for name, policy := range map[string]ServicePolicy{
		// The tmpfs sits inside the declared directory, not under it.
		"tmpfs-inside-directory": {Tmpfs: []TmpfsMount{{Target: "/state/data/cache"}},
			StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/state", Path: "data"}}}},
		"other-target": {Tmpfs: []TmpfsMount{{Target: "/run"}},
			StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/state", Path: "run"}}}},
		"sibling-prefix": {Tmpfs: []TmpfsMount{{Target: "/state/ru"}},
			StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/state", Path: "run"}}}},
		"console-home-on-volume": {Tmpfs: []TmpfsMount{{Target: "/run"}}, Console: &Console{Home: "/state/home"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := tmpfsRequest(t, "    volumes: ['state:/state']\n")
			r.Services["openlist"] = policy
			if _, err := Build(context.Background(), r); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}
