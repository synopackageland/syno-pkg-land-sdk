// Package containerpreflight checks read-only DSM container availability as the
// package account. It never opens the Docker socket or controls system services.
package containerpreflight

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// Failure exposes stable translation keys with an English fallback.
type Failure struct {
	Code       string `json:"code"`
	MessageKey string `json:"message_key"`
	Message    string `json:"message"`
}

func (f *Failure) Error() string { return fmt.Sprintf("[%s] %s", f.Code, f.Message) }
func unavailable(missing bool) error {
	if missing {
		return &Failure{"COMPOSE_CONTAINER_MANAGER_REQUIRED", "compose.containerManager.required", "Install Container Manager from Package Center, then start this App again. This NAS must support Container Manager."}
	}
	return &Failure{"COMPOSE_CONTAINER_MANAGER_NOT_READY", "compose.containerManager.notReady", "Container Manager/Docker is unavailable. Install Container Manager from Package Center if needed, start it, then start this App again."}
}

// Check uses fixed DSM-owned paths and the kernel's socket inventory. This is
// availability evidence only, not Docker/image identity or API authorization.
func Check() error { return check(os.DirFS("/")) }
func check(root fs.FS) error {
	info, err := fs.Stat(root, "var/packages/ContainerManager/INFO")
	if errors.Is(err, fs.ErrNotExist) {
		return unavailable(true)
	}
	if err != nil || !info.Mode().IsRegular() {
		return unavailable(false)
	}
	executable := false
	for _, path := range []string{"var/packages/ContainerManager/target/usr/bin/docker", "var/packages/ContainerManager/target/bin/docker"} {
		if st, err := fs.Stat(root, path); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
			executable = true
			break
		}
	}
	if !executable {
		return unavailable(false)
	}
	f, err := root.Open("proc/net/unix")
	if err != nil {
		return unavailable(false)
	}
	defer f.Close()
	const limit = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(raw) > limit {
		return unavailable(false)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 8 && fields[3] == "00010000" && fields[4] == "0001" && fields[5] == "01" && (fields[7] == "/var/run/docker.sock" || fields[7] == "/run/docker.sock") {
			return nil
		}
	}
	return unavailable(false)
}
