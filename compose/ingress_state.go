package compose

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeingress"
)

type ingressHistory struct {
	Schema      int      `json:"schema"`
	Package     string   `json:"package"`
	ProjectName string   `json:"project_name"`
	Ingress     *Ingress `json:"ingress"`
}

// This is a private record of the package lifecycle allocation, not Docker
// network ownership attestation. CM IPAM still rejects foreign network reuse.
func previousIngress(pkgvar, pkg, project string, a composeingress.Allocation, candidate int) (string, error) {
	f, err := os.OpenFile(filepath.Join(pkgvar, "compose-ingress.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", problem("COMPOSE_SUBNET_STATE_INVALID", "cannot read private ingress history")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 16384 {
		return "", problem("COMPOSE_SUBNET_STATE_INVALID", "ingress history must be a private regular file")
	}
	if native, ok := st.Sys().(*syscall.Stat_t); !ok || native.Uid != uint32(os.Geteuid()) || native.Nlink != 1 {
		return "", problem("COMPOSE_SUBNET_STATE_INVALID", "foreign ingress history")
	}
	var h ingressHistory
	if json.NewDecoder(io.LimitReader(f, 16384)).Decode(&h) != nil || h.Schema != 1 || h.Package != pkg || h.Ingress == nil {
		return "", problem("COMPOSE_SUBNET_STATE_INVALID", "invalid ingress history")
	}
	if h.ProjectName == project && h.Ingress.Allocation == a && h.Ingress.Candidate == candidate {
		return a.Subnet, nil
	}
	return "", nil
}

// RememberIngress runs in postinst, after DSM has created pkgvar. The record survives replacement/removal of the installed payload.
func RememberIngress(project, pkg, pkgvar string) error {
	if os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		return problem("COMPOSE_ROOT_FORBIDDEN", "ingress history must run as the package account")
	}
	if !packageName.MatchString(pkg) {
		return problem("COMPOSE_PACKAGE_INVALID", "invalid package")
	}
	pkgvar = packageStateDirectory(pkg, pkgvar)
	if !filepath.IsAbs(pkgvar) {
		return problem("COMPOSE_SUBNET_STATE_INVALID", "pkgvar must be absolute")
	}
	st, err := os.Stat(pkgvar)
	if err != nil || !st.IsDir() {
		return problem("COMPOSE_STORAGE_NOT_READY", "DSM must create pkgvar before ingress history")
	}
	if native, ok := st.Sys().(*syscall.Stat_t); !ok || native.Uid != uint32(os.Geteuid()) {
		return problem("COMPOSE_SUBNET_STATE_INVALID", "pkgvar must belong to the package account")
	}
	f, err := os.OpenFile(filepath.Join(project, "compose.lock.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err = f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 4<<20 {
		return problem("COMPOSE_LOCK_INVALID", "invalid ingress inventory")
	}
	var h ingressHistory
	if json.NewDecoder(io.LimitReader(f, 4<<20)).Decode(&h) != nil || h.Package != pkg {
		return problem("COMPOSE_LOCK_INVALID", "invalid ingress inventory")
	}
	if h.Ingress == nil {
		return nil
	}
	a, err := composeingress.Allocate(pkg, h.Ingress.Candidate)
	if err != nil || h.Ingress.Allocation != a || h.ProjectName == "" {
		return problem("COMPOSE_LOCK_INVALID", "invalid ingress allocation")
	}
	h.Schema = 1
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	out, err := os.CreateTemp(pkgvar, ".compose-ingress-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	if err = out.Chmod(0600); err == nil {
		_, err = out.Write(append(raw, '\n'))
	}
	if err == nil {
		err = out.Sync()
	}
	if e := out.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(out.Name(), filepath.Join(pkgvar, "compose-ingress.json")); err != nil {
		return err
	}
	dir, err := os.Open(pkgvar)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
