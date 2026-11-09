package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func tmpfsRequest(t *testing.T, service string) Request {
	t.Helper()
	r := upstreamProject(t, "openlist")
	r.Data = nil
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server']\n    read_only: true\n"+service+"volumes:\n  state: {}\n"), 0600)
	return r
}

func size(n int64) *types.UnitBytes {
	b := types.UnitBytes(n)
	return &b
}

func TestUndeclaredTmpfsIsRefusedWithDiskScratchHint(t *testing.T) {
	for name, service := range map[string]string{
		"short":            "    tmpfs: ['/tmp:mode=1777']\n",
		"short-bounded":    "    tmpfs: ['/tmp:size=64m']\n",
		"long":             "    volumes: [{type: tmpfs, target: /tmp}]\n",
		"long-bounded":     "    volumes: [{type: tmpfs, target: /tmp, tmpfs: {size: 1048576}}]\n",
		"short-string":     "    tmpfs: /tmp\n",
		"other-target-ok":  "    tmpfs: ['/tmp']\n",
		"long-mode-only":   "    volumes: [{type: tmpfs, target: /tmp, tmpfs: {mode: 0o1777}}]\n",
		"short-and-volume": "    tmpfs: ['/run']\n    volumes: ['state:/state']\n",
	} {
		t.Run(name, func(t *testing.T) {
			r := tmpfsRequest(t, service)
			if name == "other-target-ok" {
				// Declaring a different target does not cover /tmp.
				r.Services["openlist"] = ServicePolicy{Tmpfs: []TmpfsMount{{Target: "/run"}}}
			}
			_, err := Build(context.Background(), r)
			if err == nil || !strings.Contains(err.Error(), "COMPOSE_TMPFS_UNDECLARED") || !strings.Contains(err.Error(), "compose.services.openlist.scratch: [") || !strings.Contains(err.Error(), "openlist:/") {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

func TestUnlimitedTmpfsIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		service string
		policy  TmpfsMount
	}{
		"short-size-0":      {"    tmpfs: ['/tmp:size=0']\n", TmpfsMount{Target: "/tmp"}},
		"short-size-0m":     {"    tmpfs: ['/tmp:mode=1777,size=0m']\n", TmpfsMount{Target: "/tmp"}},
		"long-size-0":       {"    volumes: [{type: tmpfs, target: /tmp, tmpfs: {size: 0}}]\n", TmpfsMount{Target: "/tmp"}},
		"long-size-0-str":   {"    volumes: [{type: tmpfs, target: /tmp, tmpfs: {size: '0'}}]\n", TmpfsMount{Target: "/tmp"}},
		"declared-size-0":   {"    tmpfs: ['/tmp']\n", TmpfsMount{Target: "/tmp", Size: size(0)}},
		"declared-only-0":   {"", TmpfsMount{Target: "/run", Size: size(0)}},
		"declared-negative": {"", TmpfsMount{Target: "/run", Size: size(-1)}},
	} {
		t.Run(name, func(t *testing.T) {
			r := tmpfsRequest(t, c.service)
			r.Services["openlist"] = ServicePolicy{Tmpfs: []TmpfsMount{c.policy}}
			_, err := Build(context.Background(), r)
			if err == nil || !strings.Contains(err.Error(), "COMPOSE_TMPFS_UNBOUNDED") {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

func TestDeclaredTmpfsIsAlwaysBounded(t *testing.T) {
	for name, c := range map[string]struct {
		service string
		policy  TmpfsMount
		want    int64
	}{
		"short-default":       {"    tmpfs: ['/tmp:mode=1777']\n", TmpfsMount{Target: "/tmp"}, DefaultTmpfsBytes},
		"short-upstream-size": {"    tmpfs: ['/tmp:rw,exec,size=536870912,mode=1777']\n", TmpfsMount{Target: "/tmp"}, 536870912},
		"short-declared-size": {"    tmpfs: ['/tmp:size=1g']\n", TmpfsMount{Target: "/tmp", Size: size(16 << 20)}, 16 << 20},
		"long-default":        {"    volumes: [{type: tmpfs, target: /tmp, tmpfs: {mode: 0o1777}}]\n", TmpfsMount{Target: "/tmp"}, DefaultTmpfsBytes},
		"long-no-options":     {"    volumes: [{type: tmpfs, target: /tmp}]\n", TmpfsMount{Target: "/tmp"}, DefaultTmpfsBytes},
		"long-upstream-size":  {"    volumes: [{type: tmpfs, target: /tmp, tmpfs: {size: 1048576}}]\n", TmpfsMount{Target: "/tmp"}, 1 << 20},
		"long-declared-size":  {"    volumes: [{type: tmpfs, target: /tmp, tmpfs: {size: 1048576}}]\n", TmpfsMount{Target: "/tmp", Size: size(8 << 20)}, 8 << 20},
		"declared-only":       {"", TmpfsMount{Target: "/tmp"}, DefaultTmpfsBytes},
		"declared-only-sized": {"", TmpfsMount{Target: "/tmp", Size: size(32 << 20)}, 32 << 20},
	} {
		t.Run(name, func(t *testing.T) {
			r := tmpfsRequest(t, c.service)
			r.Services["openlist"] = ServicePolicy{Tmpfs: []TmpfsMount{c.policy}}
			p, err := Build(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			s := p.Effective.Services["openlist"]
			var got []int64
			for _, entry := range s.Tmpfs {
				target, options := shortTmpfs(entry)
				if target != "/tmp" {
					t.Fatalf("target changed: %q", entry)
				}
				sizes := 0
				for _, o := range options {
					if strings.HasPrefix(o, "size=") {
						sizes++
					}
				}
				if sizes != 1 {
					t.Fatalf("short tmpfs needs exactly one size: %q", entry)
				}
				if strings.Contains(c.service, "exec") && !slices.Contains(options, "exec") {
					t.Fatalf("other upstream options dropped: %q", entry)
				}
				got = append(got, mustSize(t, options))
			}
			for _, v := range s.Volumes {
				if v.Type == types.VolumeTypeTmpfs {
					if v.Tmpfs == nil {
						t.Fatal("unbounded tmpfs volume")
					}
					got = append(got, int64(v.Tmpfs.Size))
				}
			}
			if len(got) != 1 || got[0] != c.want {
				t.Fatalf("sizes %v, want %d", got, c.want)
			}
			if inv := p.Services[0].Tmpfs; len(inv) != 1 || inv[0].Bytes != c.want || inv[0].Target != "/tmp" {
				t.Fatalf("inventory %+v", inv)
			}
			if !strings.Contains(p.YAML, "/tmp") {
				t.Fatal("tmpfs missing from worker document")
			}
		})
	}
}

func mustSize(t *testing.T, options []string) int64 {
	t.Helper()
	var u types.UnitBytes
	for _, o := range options {
		if v, ok := strings.CutPrefix(o, "size="); ok {
			if err := json.Unmarshal([]byte(`"`+v+`"`), &u); err != nil {
				t.Fatal(err)
			}
		}
	}
	return int64(u)
}

func TestTmpfsDeclarationAcceptsTargetString(t *testing.T) {
	var p ServicePolicy
	if err := json.Unmarshal([]byte(`{"tmpfs": ["/run", {"target": "/tmp", "size": "16m"}]}`), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Tmpfs) != 2 || p.Tmpfs[0].Target != "/run" || p.Tmpfs[0].Size != nil || *p.Tmpfs[1].Size != 16<<20 {
		t.Fatalf("%+v", p.Tmpfs)
	}
	if err := json.Unmarshal([]byte(`{"tmpfs": [{"target": "/tmp", "bytes": 1}]}`), &p); err == nil {
		t.Fatal("unknown tmpfs field accepted")
	}
}

func TestScratchIsDiskBindReplacingComposeTmpfs(t *testing.T) {
	for name, service := range map[string]string{
		"replaces-short": "    tmpfs: ['/tmp:mode=1777']\n",
		"replaces-long":  "    volumes: [{type: tmpfs, target: /tmp, tmpfs: {mode: 0o1777}}]\n",
		"added":          "",
	} {
		t.Run(name, func(t *testing.T) {
			r := tmpfsRequest(t, service)
			r.Services["openlist"] = ServicePolicy{Scratch: []string{"/tmp"}, Environment: map[string]string{"TMPDIR": "/tmp"}}
			p, err := Build(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			s := p.Effective.Services["openlist"]
			if len(s.Tmpfs) != 0 {
				t.Fatalf("tmpfs kept: %v", s.Tmpfs)
			}
			var binds []types.ServiceVolumeConfig
			for _, v := range s.Volumes {
				if v.Type == types.VolumeTypeTmpfs {
					t.Fatal("tmpfs volume kept")
				}
				if v.Target == "/tmp" {
					binds = append(binds, v)
				}
			}
			want := scratchSource("Fixture", "openlist", "/tmp")
			if len(binds) != 1 || binds[0].Type != "bind" || binds[0].Source != want || binds[0].ReadOnly || binds[0].Bind == nil || bool(binds[0].Bind.CreateHostPath) {
				t.Fatalf("scratch bind %+v", binds)
			}
			if !strings.HasPrefix(want, "/var/packages/Fixture/target/compose/scratch/openlist/") {
				t.Fatalf("scratch is not in the package payload on disk: %s", want)
			}
			if !s.ReadOnly {
				t.Fatal("read_only lost")
			}
			// The SDK empties scratch before the App argv.
			if s.Entrypoint[0] != StorageInitExecutable || s.Entrypoint[1] != "--initialization" || !strings.Contains(s.Entrypoint[2], `"scratch":["/tmp"]`) || s.Entrypoint[len(s.Entrypoint)-1] != "/app/server" {
				t.Fatalf("argv %v", s.Entrypoint)
			}
			if inv := p.Services[0]; !slices.Equal(inv.Scratch, []string{"/tmp"}) || inv.StorageInit == nil || !slices.Equal(inv.StorageInit.Scratch, []string{"/tmp"}) {
				t.Fatalf("inventory %+v", inv)
			}
			if *s.Environment["TMPDIR"] != "/tmp" {
				t.Fatal("TMPDIR companion lost")
			}
		})
	}
}

func TestScratchRejectsUnsafeDeclarations(t *testing.T) {
	for name, c := range map[string]struct {
		service string
		policy  ServicePolicy
		code    string
	}{
		"volume-conflict": {"    volumes: ['state:/tmp']\n", ServicePolicy{Scratch: []string{"/tmp"}}, "COMPOSE_SCRATCH_INVALID"},
		"tmpfs-conflict":  {"", ServicePolicy{Scratch: []string{"/tmp"}, Tmpfs: []TmpfsMount{{Target: "/tmp"}}}, "COMPOSE_SCRATCH_INVALID"},
		"relative":        {"", ServicePolicy{Scratch: []string{"tmp"}}, "COMPOSE_SCRATCH_INVALID"},
		"unclean":         {"", ServicePolicy{Scratch: []string{"/tmp/../etc"}}, "COMPOSE_SCRATCH_INVALID"},
		"reserved":        {"", ServicePolicy{Scratch: []string{"/synopkgland/x"}}, "COMPOSE_SCRATCH_INVALID"},
		"duplicate":       {"", ServicePolicy{Scratch: []string{"/tmp", "/tmp"}}, "COMPOSE_SCRATCH_INVALID"},
		"root":            {"", ServicePolicy{Scratch: []string{"/tmp"}, RunAs: &RunAs{Mode: "container-root", Reason: "fixture"}}, "COMPOSE_SCRATCH_INVALID"},
		"in-storage-init": {"", ServicePolicy{StorageInit: &StorageInitialization{Scratch: []string{"/tmp"}}}, "COMPOSE_STORAGE_INIT_INVALID"},
		"too-many":        {"", ServicePolicy{Scratch: []string{"/a", "/b", "/c", "/d", "/e", "/f", "/g", "/h", "/i"}}, "COMPOSE_TMPFS_INVALID"},
	} {
		t.Run(name, func(t *testing.T) {
			r := tmpfsRequest(t, c.service)
			r.Services["openlist"] = c.policy
			_, err := Build(context.Background(), r)
			if err == nil || !strings.Contains(err.Error(), c.code) {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
	r := upstreamProject(t, "openlist")
	r.Data = nil
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n"), 0600)
	r.Services["openlist"] = ServicePolicy{Scratch: []string{"/tmp"}}
	if _, err := Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_SCRATCH_INVALID") || !strings.Contains(err.Error(), "entrypoint") {
		t.Fatalf("inherited argv: %v", err)
	}
}

func TestStorageInitOnSharedFolderIsMarkedShare(t *testing.T) {
	r := nativeRequest(t)
	r.InstallSharedFolder.Permission = "rw"
	r.Shares["media"] = Share{Select: "install", Permission: "rw", Mounts: []Mount{{Service: "openlist", Target: "/media"}}}
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server']\n    volumes: ['state:/state']\nvolumes:\n  state: {}\n"), 0600)
	// An author cannot claim a share base for package storage.
	r.Services["openlist"] = ServicePolicy{StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/media", Path: "app/home"}, {Base: "/state", Path: "app", Share: true}}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	got := p.Services[0].StorageInit.Directories
	if len(got) != 2 || !got[0].Share || got[1].Share {
		t.Fatalf("share marking %+v", got)
	}
	// Secrets and generated files stay out of a Shared Folder.
	r.Services["openlist"] = ServicePolicy{StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/media", Path: "app"}}, Runtime: &RuntimeConfiguration{Files: []RuntimeFile{{Target: "/media/app/config", Content: "x"}}}}}
	if _, err := Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_STORAGE_INIT_INVALID") {
		t.Fatalf("runtime file in a Shared Folder: %v", err)
	}
}

func TestStorageInitRuntimeOnly(t *testing.T) {
	r := tmpfsRequest(t, "")
	r.Services["openlist"] = ServicePolicy{StorageInit: &StorageInitialization{Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"GATE_KEY": {Secret: "gateway-key"}}}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	s := p.Effective.Services["openlist"]
	if s.Entrypoint[0] != StorageInitExecutable || s.Entrypoint[1] != "--initialization" || s.User != PackageUser {
		t.Fatalf("argv %v user %s", s.Entrypoint, s.User)
	}
	if strings.Contains(s.Entrypoint[2], "directories") {
		t.Fatalf("runtime-only declaration gained directories: %s", s.Entrypoint[2])
	}
	// An empty declaration still has nothing to do and is refused.
	r.Services["openlist"] = ServicePolicy{StorageInit: &StorageInitialization{}}
	if _, err := Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_STORAGE_INIT_INVALID") {
		t.Fatalf("empty storage_init: %v", err)
	}
}
