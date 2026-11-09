package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const consoleCompose = "services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/launch']\n    volumes: ['state:/run/app']\nvolumes:\n  state: {}\n"

// consoleRequest is an interactive console service with a native Share at
// /media mounted read-write, like Ovis and T3 Code.
func consoleRequest(t *testing.T) Request {
	r := nativeRequest(t)
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte(consoleCompose), 0600)
	r.InstallSharedFolder.Permission = "rw"
	s := r.Shares["media"]
	s.Permission = "rw"
	r.Shares["media"] = s
	r.Services["openlist"] = ServicePolicy{
		RunAs:   &RunAs{Mode: "package", Sudo: &Sudo{Reason: "Install build tools in the console"}},
		Console: &Console{Home: "/media/home"},
		Scratch: []string{"/scratch"},
	}
	return r
}

func TestDefaultServiceIsUnchangedByTheSudoOptIn(t *testing.T) {
	r := consoleRequest(t)
	r.Services["openlist"] = ServicePolicy{}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	s := p.Effective.Services["openlist"]
	if s.User != PackageUser || !slices.Equal(s.CapDrop, []string{"ALL"}) || len(s.CapAdd) != 0 || !slices.Equal(s.SecurityOpt, []string{"no-new-privileges:true"}) {
		t.Fatalf("default isolation changed: %+v", s)
	}
	if !slices.Equal(s.Entrypoint, []string{"/app/launch"}) || p.Services[0].Sudo != nil || p.Services[0].Console != nil {
		t.Fatal("ordinary service received the container init")
	}
	raw, _ := json.Marshal(p.Services[0])
	if strings.Contains(string(raw), "sudo") || strings.Contains(string(raw), "console") {
		t.Fatalf("default inventory gained fields: %s", raw)
	}
}

func TestSudoOptInKeepsPackageUIDAndGrantsTheFixedCapabilitySet(t *testing.T) {
	p, err := Build(context.Background(), consoleRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	s := p.Effective.Services["openlist"]
	if s.User != PackageUser {
		t.Fatalf("main process must start as the package UID, got %s", s.User)
	}
	if !slices.Equal(s.CapAdd, []string{"AUDIT_WRITE", "CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "SETGID", "SETUID"}) || !slices.Equal(s.CapDrop, []string{"ALL"}) {
		t.Fatalf("capabilities: add=%v drop=%v", s.CapAdd, s.CapDrop)
	}
	if len(s.SecurityOpt) != 0 || s.Privileged || len(s.Devices) != 0 || s.Pid != "" || s.NetworkMode == "host" {
		t.Fatalf("sudo service gained more than the fixed set: %+v", s)
	}
	if strings.Contains(p.YAML, "no-new-privileges") {
		t.Fatal("no-new-privileges must be removed only for the opted-in service")
	}
	inv := p.Services[0]
	if inv.Sudo == nil || inv.Sudo.Reason != "Install build tools in the console" || inv.ContainerRoot || inv.HostElevation || inv.User != PackageUser {
		t.Fatalf("inventory: %+v", inv)
	}
	share := false
	for _, m := range s.Volumes {
		if m.Target == "/media" && m.Type == "bind" && !m.ReadOnly {
			share = true
		}
	}
	if !share {
		t.Fatal("the selected Share must stay bound read-write for a sudo service")
	}
	lock, _ := p.LockJSON()
	if !strings.Contains(string(lock), `"reason": "Install build tools in the console"`) {
		t.Fatal("lock inventory must show the sudo reason")
	}
}

func TestSudoOnlyAppliesToTheOptedInService(t *testing.T) {
	r := consoleRequest(t)
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte(consoleCompose+"  # second\n"), 0600)
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/launch']\n    volumes: ['state:/run/app']\n  db:\n    image: postgres\nvolumes:\n  state: {}\n"), 0600)
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	db := p.Effective.Services["db"]
	if len(db.CapAdd) != 0 || !slices.Equal(db.SecurityOpt, []string{"no-new-privileges:true"}) || db.User != PackageUser {
		t.Fatalf("other service changed: %+v", db)
	}
}

func TestSudoRefusals(t *testing.T) {
	for _, kind := range []string{"no-reason", "multiline-reason", "extra-caps", "root-mode", "no-console", "scratch-tmp", "privileged", "cap_add", "devices", "host-pid", "docker-socket", "security_opt", "gpu"} {
		t.Run(kind, func(t *testing.T) {
			r := consoleRequest(t)
			policy := r.Services["openlist"]
			runAs := *policy.RunAs
			policy.RunAs = &runAs
			yaml := ""
			switch kind {
			case "no-reason":
				runAs.Sudo = &Sudo{Reason: "  "}
			case "multiline-reason":
				runAs.Sudo = &Sudo{Reason: "a\nb"}
			case "extra-caps":
				runAs.Capabilities = []string{"NET_ADMIN"}
			case "root-mode":
				runAs.Mode = "container-root"
				runAs.Reason = "x"
			case "no-console":
				policy.Console = nil
			case "scratch-tmp":
				policy.Scratch = []string{"/tmp"}
			case "privileged":
				yaml = "    privileged: true\n"
			case "cap_add":
				yaml = "    cap_add: [SYS_ADMIN]\n"
			case "devices":
				yaml = "    devices: ['/dev/fuse:/dev/fuse']\n"
			case "host-pid":
				yaml = "    pid: host\n"
			case "docker-socket":
				yaml = "    volumes: ['/var/run/docker.sock:/var/run/docker.sock']\n"
			case "security_opt":
				yaml = "    security_opt: ['seccomp:unconfined']\n"
			case "gpu":
				r.Devices = &Devices{GPU: &GPU{Services: []string{"openlist"}}}
			}
			r.Services["openlist"] = policy
			if yaml != "" {
				doc := "services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/launch']\n" + yaml
				if !strings.Contains(yaml, "volumes") {
					doc += "    volumes: ['state:/run/app']\n"
				}
				os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte(doc+"volumes:\n  state: {}\n"), 0600)
			}
			_, err := Build(context.Background(), r)
			if err == nil {
				t.Fatal("accepted")
			}
			t.Log(err)
		})
	}
}

func TestShareBindIsAllowedForSudoButNotForContainerRoot(t *testing.T) {
	r := consoleRequest(t)
	if _, err := Build(context.Background(), r); err != nil {
		t.Fatalf("sudo with a Share: %v", err)
	}
	r.Services["openlist"] = ServicePolicy{RunAs: &RunAs{Mode: "container-root", Reason: "x"}}
	if _, err := Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_ROOT_HOST_MOUNT_FORBIDDEN") {
		t.Fatalf("container-root with a Share: %v", err)
	}
}

func TestConsoleInitWrapsTheEntrypointAsThePackageUID(t *testing.T) {
	r := consoleRequest(t)
	policy := r.Services["openlist"]
	policy.StorageInit = &StorageInitialization{Directories: []WritableDirectory{{Base: "/run/app", Path: "state"}}}
	r.Services["openlist"] = policy
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if p.Services[0].Console.Tmp != "/scratch" {
		t.Fatal("TMPDIR must default to the scratch target")
	}
	s := p.Effective.Services["openlist"]
	// storage-init (package UID) prepares its dirs, then the console init, then the App.
	if s.Entrypoint[0] != StorageInitExecutable || !slices.Contains(s.Entrypoint, ConsoleExecutable) || s.Entrypoint[len(s.Entrypoint)-1] != "/app/launch" {
		t.Fatalf("entrypoint: %v", s.Entrypoint)
	}
	i := slices.Index(s.Entrypoint, ConsoleExecutable)
	if !slices.Equal(s.Entrypoint[i+1:i+4], []string{"container", "init", "--config"}) {
		t.Fatalf("console argv: %v", s.Entrypoint[i:])
	}
	var c ConsoleConfig
	if err := json.Unmarshal([]byte(s.Entrypoint[i+4]), &c); err != nil || c.Home != "/media/home" || c.Tmp != "/scratch" || c.User != "fixture" || !c.Sudo {
		t.Fatalf("config %+v %v", c, err)
	}
	if s.User != PackageUser {
		t.Fatal("SDK-prepared dirs must be made by the package UID")
	}
	mounted := false
	for _, m := range s.Volumes {
		if m.Target == ConsoleExecutable && m.ReadOnly && m.Source == "/var/packages/Fixture/target/compose/storage-init" {
			mounted = true
		}
	}
	if !mounted || p.Services[0].Console == nil || p.Services[0].Console.User != "fixture" {
		t.Fatal("console binary or inventory missing")
	}
}

func TestConsoleRefusals(t *testing.T) {
	for _, kind := range []string{"home-unmounted", "home-readonly", "home-relative", "tmp-unmounted", "user", "root", "no-entrypoint", "reserved"} {
		t.Run(kind, func(t *testing.T) {
			r := consoleRequest(t)
			c := *r.Services["openlist"].Console
			policy := ServicePolicy{Console: &c}
			switch kind {
			case "home-unmounted":
				c.Home = "/home/app"
			case "home-readonly":
				s := r.Shares["media"]
				s.Permission = "ro"
				r.Shares["media"] = s
				r.InstallSharedFolder.Permission = "ro"
			case "home-relative":
				c.Home = "home"
			case "tmp-unmounted":
				c.Tmp = "/run/app/tmp"
			case "user":
				c.User = "Root:0"
			case "root":
				policy.RunAs = &RunAs{Mode: "container-root", Reason: "x"}
				r.Shares = nil
				r.InstallSharedFolder = nil
				r.Data = nil
				c.Home = "/run/app/home"
			case "no-entrypoint":
				os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    volumes: ['state:/run/app']\nvolumes:\n  state: {}\n"), 0600)
			case "reserved":
				os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/launch']\n    volumes: ['state:/run/app', 'bin:/synopkgland/bin']\nvolumes:\n  state: {}\n  bin: {}\n"), 0600)
			}
			r.Services["openlist"] = policy
			_, err := Build(context.Background(), r)
			if err == nil {
				t.Fatal("accepted")
			}
			t.Log(err)
		})
	}
}

func TestConsoleTmpDefaultsToScratchAndIsOptional(t *testing.T) {
	r := consoleRequest(t)
	policy := r.Services["openlist"]
	policy.Scratch = []string{"/var/scratch", "/scratch"}
	c := *policy.Console
	c.Tmp = "/scratch/agent"
	policy.Console = &c
	r.Services["openlist"] = policy
	p, err := Build(context.Background(), r)
	if err != nil || p.Services[0].Console.Tmp != "/scratch/agent" {
		t.Fatalf("declared tmp below scratch: %v", err)
	}
	policy.Scratch = nil
	c.Tmp = ""
	r.Services["openlist"] = policy
	p, err = Build(context.Background(), r)
	if err != nil || p.Services[0].Console.Tmp != "" {
		t.Fatalf("console without scratch: %v", err)
	}
}
