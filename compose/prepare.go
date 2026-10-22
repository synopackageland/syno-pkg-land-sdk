package compose

import (
	"encoding/json"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeingress"
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
	var directories []string
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
	_, err = fmt.Fprintf(f, "SYNOPKGLAND_UID=%s\nSYNOPKGLAND_GID=%s\nSYNOPKG_PKGVAR=%s\n%s", strconv.Itoa(os.Geteuid()), strconv.Itoa(os.Getegid()), dotenvLiteral(pkgvar), shareEnvironment)
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
	if explicit != "" {
		return explicit
	}
	if value := os.Getenv("SYNOPKG_PKGVAR"); value != "" {
		return value
	}
	// DSM7 preinst has only the destination volume, before pkgvar/symlinks exist.
	if volume := os.Getenv("SYNOPKG_PKGDEST_VOL"); volume != "" {
		return filepath.Join(volume, "@appdata", pkg)
	}
	return filepath.Join("/var/packages", pkg, "var")
}
