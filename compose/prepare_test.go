package compose

import (
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeingress"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPrepareWritesOnlyPrivateIdentityAndRefusesUnsafeEnv(t *testing.T) {
	dir := t.TempDir()
	if err := Prepare(dir, "Fixture", ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "SYNOPKGLAND_UID="+strconv.Itoa(os.Getuid())) {
		t.Fatal(string(b))
	}
	st, _ := os.Stat(filepath.Join(dir, ".env"))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	os.Remove(filepath.Join(dir, ".env"))
	os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(dir, ".env"))
	if err = Prepare(dir, "Fixture", ""); err == nil {
		t.Fatal("symlink env accepted")
	}
}

func TestPrepareCannotCreateTheDSMOwnedPackageVar(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(t.TempDir(), "pkgvar")
	if err := Prepare(dir, "Fixture", absent); err == nil || !strings.Contains(err.Error(), "COMPOSE_STORAGE_NOT_READY") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("created DSM-owned root")
	}
}

func TestIngressHistorySurvivesRemovalOfTheInstalledPayload(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("package account required")
	}
	project, pkgvar := t.TempDir(), t.TempDir()
	a, _ := composeingress.Allocate("Fixture", 0)
	record := ingressHistory{Schema: 1, Package: "Fixture", ProjectName: "fixture", Ingress: &Ingress{Allocation: a, Service: "web", Port: 8080}}
	raw, _ := json.Marshal(record)
	os.WriteFile(filepath.Join(project, "compose.lock.json"), raw, 0600)
	if err := RememberIngress(project, "Fixture", pkgvar); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(project)
	previous, err := previousIngress(pkgvar, "Fixture", "fixture", a, 0)
	if err != nil || previous != a.Subnet {
		t.Fatalf("lost allocation after payload removal: %q %v", previous, err)
	}
	if got, err := previousIngress(pkgvar, "Fixture", "different-project", a, 0); err != nil || got != "" {
		t.Fatal("foreign resource prefix reused old network")
	}
	if err := os.Chmod(filepath.Join(pkgvar, "compose-ingress.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := previousIngress(pkgvar, "Fixture", "fixture", a, 0); err == nil {
		t.Fatal("nonprivate history accepted")
	}
}

func TestPrepareReadsDSMPhysicalVarWhenPackageSymlinkIsAbsent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("package account required")
	}
	project, pkgvar := t.TempDir(), t.TempDir()
	a, _ := composeingress.Allocate("Fixture", 0)
	h := ingressHistory{Schema: 1, Package: "Fixture", ProjectName: "fixture", Ingress: &Ingress{Allocation: a, Service: "web", Port: 8080}}
	raw, _ := json.Marshal(h)
	os.WriteFile(filepath.Join(project, "compose.lock.json"), raw, 0600)
	os.WriteFile(filepath.Join(pkgvar, "compose-ingress.json"), raw, 0644)
	t.Setenv("SYNOPKG_PKGVAR", pkgvar)
	if err := Prepare(project, "Fixture", ""); err == nil || !strings.Contains(err.Error(), "COMPOSE_SUBNET_STATE_INVALID") {
		t.Fatalf("ignored DSM state path: %v", err)
	}
	os.Chmod(filepath.Join(pkgvar, "compose-ingress.json"), 0600)
	if err := Prepare(project, "Fixture", ""); err != nil {
		t.Fatal(err)
	}
}

func TestPreinstUsesDestinationVolumeWithoutPkgvarEnvironment(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("package account required")
	}
	volume, project := t.TempDir(), t.TempDir()
	pkgvar := filepath.Join(volume, "@appdata", "Fixture")
	os.MkdirAll(pkgvar, 0700)
	a, _ := composeingress.Allocate("Fixture", 0)
	h := ingressHistory{Schema: 1, Package: "Fixture", ProjectName: "fixture", Ingress: &Ingress{Allocation: a, Service: "web", Port: 8080}}
	raw, _ := json.Marshal(h)
	os.WriteFile(filepath.Join(project, "compose.lock.json"), raw, 0600)
	os.WriteFile(filepath.Join(pkgvar, "compose-ingress.json"), raw, 0644)
	t.Setenv("SYNOPKG_PKGVAR", "")
	t.Setenv("SYNOPKG_PKGDEST_VOL", volume)
	if err := Prepare(project, "Fixture", ""); err == nil || !strings.Contains(err.Error(), "COMPOSE_SUBNET_STATE_INVALID") {
		t.Fatalf("ignored physical DSM7 state: %v", err)
	}
	os.Chmod(filepath.Join(pkgvar, "compose-ingress.json"), 0600)
	if err := Prepare(project, "Fixture", ""); err != nil {
		t.Fatal(err)
	}
}
