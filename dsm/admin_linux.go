//go:build linux

package dsm

import (
	"context"
	_ "embed"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// The bridge is compiled reproducibly from native/admin_check.c. Embedding
// binds it to the CGI's build; no writable helper path or runtime library path.
//
//go:embed native/admin-x86_64.elf
var adminAMD64 []byte

//go:embed native/admin-aarch64.elf
var adminARM64 []byte

//go:embed native/apppriv-x86_64.elf
var appPrivAMD64 []byte

//go:embed native/apppriv-aarch64.elf
var appPrivARM64 []byte

//go:embed native/admin-armv7.elf
var adminARMv7 []byte

//go:embed native/apppriv-armv7.elf
var appPrivARMv7 []byte

//go:embed native/account-x86_64.elf
var accountAMD64 []byte

//go:embed native/account-aarch64.elf
var accountARM64 []byte

//go:embed native/account-armv7.elf
var accountARMv7 []byte

// Bridges per runtime.GOARCH; an architecture without one is refused.
var (
	adminBridges   = map[string][]byte{"amd64": adminAMD64, "arm64": adminARM64, "arm": adminARMv7}
	appPrivBridges = map[string][]byte{"amd64": appPrivAMD64, "arm64": appPrivARM64, "arm": appPrivARMv7}
	accountBridges = map[string][]byte{"amd64": accountAMD64, "arm64": accountARM64, "arm": accountARMv7}
)

func nativeAdministrator(ctx context.Context, principal string) (bool, error) {
	out, err := runBridge(ctx, adminBridges, principal)
	return parseNativeRole(out, err)
}

func nativeAppPrivilege(ctx context.Context, principal, app, addr string) (bool, error) {
	out, err := runBridge(ctx, appPrivBridges, principal, app, addr)
	return parseNativeBit(out, err, errNativeAppPriv)
}

func nativeAccountEnabled(ctx context.Context, principal string) (bool, error) {
	// Up to 1024 group IDs and a home path follow the fields read here.
	out, err := runBridgeLimit(ctx, accountBridges, 32768, principal)
	return parseNativeAccount(out, principal, err)
}

// runBridge executes the sealed bridge for this architecture with fixed
// arguments and a minimal environment, returning at most 16 bytes of output.
func runBridge(ctx context.Context, bridges map[string][]byte, args ...string) ([]byte, error) {
	return runBridgeLimit(ctx, bridges, 16, args...)
}

func runBridgeLimit(ctx context.Context, bridges map[string][]byte, limit int, args ...string) ([]byte, error) {
	payload, ok := bridges[runtime.GOARCH]
	if !ok {
		return nil, errors.New("unsupported architecture")
	}
	file, err := sealedBridge(payload)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	cmd.ExtraFiles = []*os.File{file}
	// In particular, never inherit LD_PRELOAD/LD_LIBRARY_PATH or credentials.
	cmd.Env = []string{"LANG=C", "LC_ALL=C"}
	return commandOutput(cmd, limit)
}

// memfdCreate is the sealing primitive; tests replace it to model old kernels.
var memfdCreate = unix.MemfdCreate

func sealedBridge(payload []byte) (*os.File, error) {
	fd, err := memfdCreate("synopkgland-admin", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if errors.Is(err, unix.ENOSYS) {
		return unlinkedBridge(bridgeFallbackDir, payload)
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "synopkgland-admin")
	fail := func(err error) (*os.File, error) { file.Close(); return nil, err }
	if _, err = file.Write(payload); err != nil {
		return fail(err)
	}
	if err = unix.Fchmod(fd, 0500); err != nil {
		return fail(err)
	}
	if _, err = unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		return fail(err)
	}
	if _, err = file.Seek(0, 0); err != nil {
		return fail(err)
	}
	return file, nil
}

func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
