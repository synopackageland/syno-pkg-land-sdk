package compose

import (
	"encoding/json"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/installsettings"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeingress"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/pkgstate"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Prepare runs only as the package account. It never creates the DSM-owned
// pkgvar root, chowns data, invokes a shell, or opens a container control socket.
// DSM storage/resource timing remains an explicit device acceptance condition.
func Prepare(project, pkg, pkgvar string) error {
	if os.Getuid() == 0 || os.Getuid() != os.Geteuid() {
		return problem("COMPOSE_ROOT_FORBIDDEN", "prepare must run as the package account")
	}
	if !packageName.MatchString(pkg) {
		return problem("COMPOSE_PACKAGE_INVALID", "invalid package")
	}
	st, err := os.Lstat(project)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return problem("COMPOSE_SOURCE_UNSAFE", "project must be a regular directory")
	}
	if native, ok := st.Sys().(*syscall.Stat_t); !ok || native.Uid != uint32(os.Geteuid()) {
		return problem("COMPOSE_SOURCE_UNSAFE", "project owner must be the package account")
	}
	explicit := pkgvar != ""
	pkgvar = packageStateDirectory(pkg, pkgvar)
	if !filepath.IsAbs(pkgvar) || strings.ContainsAny(pkgvar, "\r\n\x00") {
		return problem("COMPOSE_STORAGE_NOT_READY", "invalid package var path")
	}
	var directories, scratch []string
	var shareEnvironment string
	if raw, err := os.ReadFile(filepath.Join(project, "compose.lock.json")); err == nil {
		var lock struct {
			Devices             *DeviceInventory     `json:"devices"`
			SHA256              string               `json:"effective_sha256"`
			Package             string               `json:"package"`
			Services            []Inventory          `json:"services"`
			MountsBlocked       bool                 `json:"mounts_blocked"`
			Ingress             *Ingress             `json:"ingress"`
			ProjectName         string               `json:"project_name"`
			InstallSharedFolder *InstallSharedFolder `json:"install_shared_folder"`
		}
		if len(raw) > 4<<20 || json.Unmarshal(raw, &lock) != nil || lock.Package != pkg {
			return problem("COMPOSE_LOCK_INVALID", "private storage inventory is invalid")
		}
		if lock.Devices != nil {
			account, err := user.Lookup(pkg)
			if err != nil || account.Uid != strconv.Itoa(os.Geteuid()) || account.Gid != strconv.Itoa(os.Getegid()) {
				return problem("COMPOSE_GPU_INVALID", "GPU materialization requires the matching native package UID:GID")
			}
		}
		if err := prepareGPU(project, lock.Devices, lock.SHA256); err != nil {
			return err
		}
		if lock.InstallSharedFolder != nil {
			account, err := user.Lookup(pkg)
			if err != nil || account.Uid != strconv.Itoa(os.Geteuid()) || account.Gid != strconv.Itoa(os.Getegid()) || lock.MountsBlocked {
				return problem("COMPOSE_SHARE_INVALID", "native shares require the matching package UID:GID and an enabled native plan")
			}
			shareEnvironment, err = nativeShareEnvironment(lock.InstallSharedFolder, pkgvar, "/")
			if err != nil {
				return err
			}
		}
		if err := prepareRuntimeSecrets(project, pkg, pkgvar, lock.Services); err != nil {
			return err
		}
		if lock.Ingress != nil {
			allocation, err := composeingress.Allocate(pkg, lock.Ingress.Candidate)
			if err != nil || allocation != lock.Ingress.Allocation {
				return problem("COMPOSE_LOCK_INVALID", "invalid ingress allocation")
			}
			previous, err := previousIngress(pkgvar, pkg, lock.ProjectName, allocation, lock.Ingress.Candidate)
			if err != nil {
				return err
			}

			routes, err := os.ReadFile("/proc/net/route")
			if err != nil {
				return problem("COMPOSE_SUBNET_INVENTORY_UNAVAILABLE", "cannot read kernel route inventory")
			}
			if err = composeingress.CheckRoutes(string(routes), allocation.Subnet, previous); err != nil {
				return err
			}
			interfaces, err := net.Interfaces()
			if err != nil {
				return problem("COMPOSE_SUBNET_INVENTORY_UNAVAILABLE", "cannot read interface inventory")
			}
			addresses := map[string][]string{}
			for _, iface := range interfaces {
				values, err := iface.Addrs()
				if err != nil {
					return problem("COMPOSE_SUBNET_INVENTORY_UNAVAILABLE", "cannot read interface prefixes")
				}
				for _, value := range values {
					addresses[iface.Name] = append(addresses[iface.Name], value.String())
				}
			}
			if err = composeingress.CheckAddresses(addresses, allocation.Subnet, previous); err != nil {
				return err
			}

		}
		scratchPrefix := "/var/packages/" + pkg + "/target/compose/"
		for _, s := range lock.Services {
			for _, v := range s.Mounts {
				if rel, ok := strings.CutPrefix(v.Source, scratchPrefix+ScratchDirectory+"/"); ok {
					if !safeRelative(rel) || strings.Count(rel, "/") != 1 {
						return problem("COMPOSE_SCRATCH_NOT_READY", "unsafe scratch directory")
					}
					scratch = append(scratch, ScratchDirectory+"/"+rel)
				}
			}
		}
		if !lock.MountsBlocked {
			for _, s := range lock.Services {
				for _, v := range s.Mounts {
					if strings.HasPrefix(v.Source, "${SYNOPKG_PKGVAR}/") {
						rel := strings.TrimPrefix(v.Source, "${SYNOPKG_PKGVAR}/")
						if !safeRelative(rel) {
							return problem("COMPOSE_STORAGE_NOT_READY", "unsafe private directory")
						}
						directories = append(directories, rel)
					}
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if explicit || len(directories) > 0 {
		varst, err := os.Stat(pkgvar)
		if err != nil || !varst.IsDir() {
			return problem("COMPOSE_STORAGE_NOT_READY", "DSM must create and own pkgvar before private binds can be initialized; the package will not create it")
		}
		if native, ok := varst.Sys().(*syscall.Stat_t); !ok || native.Uid != uint32(os.Geteuid()) {
			return problem("COMPOSE_STORAGE_NOT_READY", "pkgvar is not owned by the package account")
		}
		root, err := os.OpenRoot(pkgvar)
		if err != nil {
			return err
		}
		defer root.Close()
		for _, rel := range directories {
			parts := strings.Split(rel, "/")
			for i := range parts {
				path := filepath.Join(parts[:i+1]...)
				st, err := root.Lstat(path)
				if err == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
					return problem("COMPOSE_STORAGE_NOT_READY", "private bind path contains a non-directory or symlink")
				}

				if err == nil {
					native, ok := st.Sys().(*syscall.Stat_t)
					if !ok || native.Uid != uint32(os.Geteuid()) || st.Mode().Perm()&0077 != 0 {
						return problem("COMPOSE_STORAGE_NOT_READY", "private bind directory must be owned by the package account with private permissions")
					}
				}
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				if os.IsNotExist(err) {
					if err = root.Mkdir(path, 0700); err != nil {
						return err
					}
				}
			}
		}
	}
	if err := prepareScratch(project, scratch); err != nil {
		return err
	}
	envPath := filepath.Join(project, ".env")
	if st, err := os.Lstat(envPath); err == nil && (!st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0) {
		return problem("COMPOSE_SOURCE_UNSAFE", "runtime env must not be a symlink or special file")
	}
	f, err := os.CreateTemp(project, ".env.tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	portValues, portErr := installsettings.PortValues(filepath.Dir(project))
	if portErr != nil {
		f.Close()
		return portErr
	}
	var portEnvironment strings.Builder
	for key, n := range portValues {
		fmt.Fprintf(&portEnvironment, "SYNOPKGLAND_PORT_%s=%d\n", strings.ToUpper(key), n)
	}
	_, err = fmt.Fprintf(f, "SYNOPKGLAND_UID=%s\nSYNOPKGLAND_GID=%s\nSYNOPKG_PKGVAR=%s\n%s", strconv.Itoa(os.Geteuid()), strconv.Itoa(os.Getegid()), dotenvLiteral(pkgvar), shareEnvironment+portEnvironment.String())
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), envPath)
}

// DSM removes /var/packages/<id> before upgrade preinst. Its physical pkgvar
// environment path remains available even while that convenience symlink is gone.
func packageStateDirectory(pkg, explicit string) string {
	return pkgstate.Directory(pkg, explicit)
}

// prepareScratch creates each disk scratch directory in the incoming payload
// as the package account. It is created fresh and explicitly set to 0700, so
// neither umask nor inherited permissions decide the mode the App sees.
func prepareScratch(project string, scratch []string) error {
	if len(scratch) == 0 {
		return nil
	}
	root, err := os.OpenRoot(project)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.RemoveAll(ScratchDirectory); err != nil {
		return problem("COMPOSE_SCRATCH_NOT_READY", "cannot reset scratch: %v", err)
	}
	for _, rel := range scratch {
		parts := strings.Split(rel, "/")
		for i := range parts {
			dir := filepath.Join(parts[:i+1]...)
			if err := root.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
				return problem("COMPOSE_SCRATCH_NOT_READY", "cannot create scratch: %v", err)
			}
			if err := root.Chmod(dir, 0700); err != nil {
				return problem("COMPOSE_SCRATCH_NOT_READY", "cannot set scratch mode: %v", err)
			}
			st, err := root.Lstat(dir)
			if err != nil {
				return err
			}
			native, ok := st.Sys().(*syscall.Stat_t)
			if !st.IsDir() || !ok || native.Uid != uint32(os.Geteuid()) || st.Mode().Perm() != 0700 {
				return problem("COMPOSE_SCRATCH_NOT_READY", "scratch must be a package-owned 0700 directory")
			}
		}
	}
	return nil
}
