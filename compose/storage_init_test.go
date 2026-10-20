//go:build linux

package compose

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeStoragePrivateAndPersistent(t *testing.T) {
	base := t.TempDir()
	declaration := []WritableDirectory{{Base: base, Path: "app/data"}}
	if err := InitializeStorage(declaration); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(base, "app/data")
	st, err := os.Stat(data)
	if err != nil || st.Mode().Perm() != 0700 {
		t.Fatalf("private directory: %v %v", st, err)
	}
	if err := os.WriteFile(filepath.Join(data, "database"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStorage(declaration); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(data, "database")); err != nil || string(b) != "retained" {
		t.Fatalf("upgrade lost data: %q %v", b, err)
	}
}

func TestInitializeStorageFailsClosed(t *testing.T) {
	for _, kind := range []string{"unwritable-base", "unwritable-data", "symlink", "public-data", "traversal", "missing-base"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			d := WritableDirectory{Base: base, Path: "data"}
			switch kind {
			case "unwritable-base":
				os.Chmod(base, 0500)
				defer os.Chmod(base, 0700)
			case "unwritable-data":
				os.Mkdir(filepath.Join(base, "data"), 0500)
			case "symlink":
				os.Symlink(t.TempDir(), filepath.Join(base, "data"))
			case "public-data":
				os.Mkdir(filepath.Join(base, "data"), 0777)
				os.Chmod(filepath.Join(base, "data"), 0777)
			case "traversal":
				d.Path = "../escape"
			case "missing-base":
				d.Base = filepath.Join(base, "missing")
			}
			err := InitializeStorage([]WritableDirectory{d})
			if err == nil || !strings.Contains(err.Error(), "COMPOSE_STORAGE_NOT_WRITABLE") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}

func TestStorageInitPlannerPreservesExplicitAppArgv(t *testing.T) {
	r := upstreamProject(t, "openlist")
	// Use a named volume; private host binds remain subject to DSM timing.
	r.Data = nil
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server', '--listen']\n    command: ['literal $$value']\n    volumes: ['state:/state']\nvolumes:\n  state: {}\n"), 0600)
	r.Services["openlist"] = ServicePolicy{StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/state", Path: "app"}}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	s := p.Effective.Services["openlist"]
	if s.Entrypoint[0] != "/synopkgland/storage-init" || s.Entrypoint[len(s.Entrypoint)-2] != "/app/server" || s.Entrypoint[len(s.Entrypoint)-1] != "--listen" || len(s.Command) != 1 || s.Command[0] != "literal $value" {
		t.Fatalf("argv changed: %v %v", s.Entrypoint, s.Command)
	}
	mounted := false
	for _, m := range s.Volumes {
		if m.Target == "/synopkgland/storage-init" && m.Source == "/var/packages/Fixture/target/compose/storage-init" && m.ReadOnly && m.Bind != nil && m.Bind.CreateHostPath == false {
			mounted = true
		}
	}
	if !mounted {
		t.Fatal("official image needs the SDK initializer from a read-only SPK payload mount")
	}
	if !strings.Contains(p.YAML, "storage-init") || s.User != PackageUser {
		t.Fatal("missing nonroot preflight")
	}
}

func TestStorageInitPlannerRejectsUnsafeDeclarations(t *testing.T) {
	for _, kind := range []string{"unmounted", "readonly", "traversal", "root", "inherited-argv", "hidden-helper", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			r := upstreamProject(t, "openlist")
			r.Data = nil
			source := "services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server']\n    volumes: ['state:/state']\nvolumes:\n  state: {}\n"
			init := &StorageInitialization{Directories: []WritableDirectory{{Base: "/state", Path: "app"}}}
			policy := ServicePolicy{StorageInit: init}
			switch kind {
			case "unmounted":
				init.Directories[0].Base = "/missing"
			case "readonly":
				source = strings.Replace(source, "state:/state", "state:/state:ro", 1)
			case "traversal":
				init.Directories[0].Path = "../data"
			case "root":
				policy.RunAs = &RunAs{Mode: "container-root", Reason: "fixture"}
			case "inherited-argv":
				source = strings.Replace(source, "    entrypoint: ['/app/server']\n", "    command: ['argument-for-image-entrypoint']\n", 1)
			case "hidden-helper":
				source = strings.Replace(source, "state:/state", "state:/state, state:/synopkgland", 1)
			case "duplicate":
				init.Directories = append(init.Directories, init.Directories[0])
			}
			os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte(source), 0600)
			r.Services["openlist"] = policy
			_, err := Build(context.Background(), r)
			if err == nil || !strings.Contains(err.Error(), "COMPOSE_STORAGE_INIT_INVALID") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}
