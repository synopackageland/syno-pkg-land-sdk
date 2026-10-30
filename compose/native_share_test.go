package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
)

func nativeRequest(t *testing.T) Request {
	r := upstreamProject(t, "openlist")
	r.ComposeMounts = true
	r.InstallSharedFolder = &InstallSharedFolder{Mode: "select", Key: "media", Permission: "ro"}
	r.Shares = map[string]Share{"media": {Select: "install", Mounts: []Mount{{Service: "openlist", Target: "/media"}}}}
	return r
}

func TestNativeSharePlanUsesPackageIdentityAndKeepsDynamicAuthorityBlocked(t *testing.T) {
	r := nativeRequest(t)
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if p.MountsBlocked {
		t.Fatal("native install is blocked")
	}
	s := p.Effective.Services["openlist"]
	if s.User != PackageUser {
		t.Fatal("lost fixed identity")
	}
	var found bool
	for _, v := range s.Volumes {
		if v.Target == "/media" {
			found = true
			if !v.ReadOnly || v.Source != "${SYNOPKGLAND_MOUNT_media}" || v.Bind == nil || bool(v.Bind.CreateHostPath) {
				t.Fatalf("unsafe native mount %+v", v)
			}
		}
	}
	if !found {
		t.Fatal("missing native mount")
	}
	raw, _ := p.LockJSON()
	var lock struct {
		InstallSharedFolder *InstallSharedFolder `json:"install_shared_folder"`
	}
	if json.Unmarshal(raw, &lock) != nil || lock.InstallSharedFolder == nil || *lock.InstallSharedFolder != *r.InstallSharedFolder {
		t.Fatal("native selection missing from lock")
	}
	r.Shares["media"] = Share{Select: "admin-after-install", Permission: "ro", Mounts: []Mount{{Service: "openlist", Target: "/media"}}}
	r.InstallSharedFolder = nil
	p, err = Build(context.Background(), r)
	if err != nil || !p.MountsBlocked {
		t.Fatalf("dynamic authority was enabled: %v", err)
	}
}

func TestNativeShareRejectsMismatchedAuthorityAndRoot(t *testing.T) {
	for _, which := range []string{"missing", "key", "permission", "mode", "root", "capability", "mixed"} {
		t.Run(which, func(t *testing.T) {
			r := nativeRequest(t)
			switch which {
			case "missing":
				r.InstallSharedFolder = nil
			case "key":
				r.InstallSharedFolder.Key = "other"
			case "permission":
				s := r.Shares["media"]
				s.Permission = "rw"
				r.Shares["media"] = s
			case "mode":
				r.InstallSharedFolder.Mode = "create"
			case "root":
				r.Services["openlist"] = ServicePolicy{RunAs: &RunAs{Mode: "container-root", Reason: "test"}}
			case "capability":
				r.ComposeMounts = false
			case "mixed":
				r.Shares["other"] = Share{Select: "admin-after-install", Permission: "ro", Mounts: []Mount{{Service: "openlist", Target: "/other"}}}
			}
			if _, err := Build(context.Background(), r); err == nil {
				t.Fatal("accepted invalid native authority")
			}
		})
	}
}

func TestNativeShareMaterializationUsesWizardOrPersistedName(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	name := "Media $HOME ' library"
	path := filepath.Join(root, "volume1", name)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	f := &InstallSharedFolder{Mode: "select", Key: "media", Permission: "ro"}
	writeConfig := func(name string) {
		raw, _ := json.Marshal(map[string]any{"schema": 1, "shared_folders": map[string]string{"media": name}})
		os.WriteFile(filepath.Join(data, "config.json"), raw, 0600)
	}
	writeConfig(name)
	t.Setenv("SYNOPKG_PKG_STATUS", "UPGRADE")
	t.Setenv("media", "unrelated prefilled selection")
	got, err := nativeShareEnvironment(f, data, root)
	if err != nil {
		t.Fatal(err)
	}
	env, err := dotenv.Parse(strings.NewReader(got))
	if err != nil || env["SYNOPKGLAND_MOUNT_media"] != path {
		t.Fatalf("dotenv interpolation changed selection: %q %v", env, err)
	}
	t.Setenv("CHANGE_media", "true")
	if _, err := nativeShareEnvironment(f, data, root); err == nil {
		t.Fatal("missing upgraded source accepted")
	}
	t.Setenv("media", name)
	if _, err := nativeShareEnvironment(f, data, root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	t.Setenv("media", "../../etc")
	if _, err := nativeShareEnvironment(f, data, root); err == nil {
		t.Fatal("traversal accepted")
	}
	t.Setenv("media", name)
	os.MkdirAll(filepath.Join(root, "volume2", name), 0700)
	if _, err := nativeShareEnvironment(f, data, root); err == nil {
		t.Fatal("ambiguous source accepted")
	}
	os.RemoveAll(filepath.Join(root, "volume2"))
	os.Remove(path)
	os.Symlink(data, path)
	if _, err := nativeShareEnvironment(f, data, root); err == nil {
		t.Fatal("symlink source accepted")
	}
}

func TestDedicatedSharePlansWithoutRootAndDefaultsBeforeResourceAcquisition(t *testing.T) {
	r := nativeRequest(t)
	r.InstallSharedFolder.Mode = "dedicated"
	r.InstallSharedFolder.Name = "Reader"
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if p.MountsBlocked || p.InstallSharedFolder.Name != "Reader" {
		t.Fatalf("%+v", p)
	}
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	t.Setenv("SYNOPKG_PKGINST_TEMP_DIR", t.TempDir())
	t.Setenv("SYNOPKG_PKGDEST_VOL", "/volume3")
	t.Setenv("media", "")
	env, err := nativeShareEnvironment(r.InstallSharedFolder, t.TempDir(), t.TempDir())
	if err != nil || env != "SYNOPKGLAND_MOUNT_media='/volume3/Reader'\n" {
		t.Fatalf("%q %v", env, err)
	}
	t.Setenv("SYNOPKG_PKGINST_TEMP_DIR", "")
	if _, err = nativeShareEnvironment(r.InstallSharedFolder, t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("missing dedicated share accepted outside installer")
	}
}
