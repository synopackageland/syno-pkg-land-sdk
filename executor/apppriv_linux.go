//go:build linux

package main

import (
	"context"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/budget"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/memoryexec"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"
)

// appPrivBridge runs the embedded SLIBAppPrivUserHas bridge. Dir is the
// executor's validated root-only directory. Kernels without memfd_create also
// require executable tmpfs; an empty Dir refuses that fallback.
type appPrivBridge struct {
	Dir      string
	payload  []byte // tests only; nil is this architecture's bridge
	prepared bool
	file     *os.File
	err      error
}

// Prepare once while starting the service. On old kernels the sole disk-backed
// memory staging happens here; every subsequent check reopens the unlinked,
// read-only descriptor through procfs. There is no per-request staging retry.
func (b appPrivBridge) prepare(payload []byte) appPrivBridge {
	b.file, b.err = b.stage(payload)
	b.prepared = true
	return b
}

func preparedAppPrivBridge(dir string) appPrivBridge {
	return (appPrivBridge{Dir: dir}).prepare(appPrivBridges[runtime.GOARCH])
}

// Check asks DSM whether principal may use app from addr. The arguments were
// validated by the caller; the bridge refuses empty or oversized ones itself.
func (b appPrivBridge) Check(ctx context.Context, principal, app, addr string) (bool, error) {
	payload := b.payload
	if payload == nil {
		var ok bool
		if payload, ok = appPrivBridges[runtime.GOARCH]; !ok {
			return false, errAppPriv
		}
	}
	file, err := b.stage(payload)
	if err != nil {
		return false, errAppPriv
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(ctx, budget.Operation)
	defer cancel()
	cmd := command(ctx, "/proc/self/fd/3", []string{principal, app, addr})
	cmd.ExtraFiles = []*os.File{file}
	out := &boundedOutput{limit: 16}
	cmd.Stdout = out
	return parseAppPriv(out.buf, cmd.Run())
}

type boundedOutput struct {
	buf   []byte
	limit int
}

func (o *boundedOutput) Write(p []byte) (int, error) {
	if len(o.buf)+len(p) > o.limit {
		return 0, errors.New("bridge output limit")
	}
	o.buf = append(o.buf, p...)
	return len(p), nil
}

// memfd_create per runtime.GOARCH; the standard library has no wrapper.
var memfdCreateTrap = map[string]uintptr{"amd64": 319, "arm64": 279, "arm": 385}

// memfdCreate is the sealing primitive; tests replace it to model old kernels.
var memfdCreate = func() (int, error) {
	trap, ok := memfdCreateTrap[runtime.GOARCH]
	if !ok {
		return -1, syscall.ENOSYS
	}
	name, _ := syscall.BytePtrFromString("synopkgland-apppriv")
	const cloexecAllowSealing = 0x1 | 0x2 // MFD_CLOEXEC | MFD_ALLOW_SEALING
	fd, _, errno := syscall.Syscall(trap, uintptr(unsafe.Pointer(name)), cloexecAllowSealing, 0)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

// stage returns a read-only executable descriptor holding payload: a sealed
// memfd, or without one a file in Dir whose name is already gone.
func (b appPrivBridge) stage(payload []byte) (*os.File, error) {
	if b.prepared {
		if b.err != nil {
			return nil, b.err
		}
		return os.Open("/proc/self/fd/" + strconv.FormatUint(uint64(b.file.Fd()), 10))
	}
	fd, err := memfdCreate()
	if errors.Is(err, syscall.ENOSYS) {
		return unlinkedBridge(b.Dir, payload)
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "synopkgland-apppriv")
	fail := func(err error) (*os.File, error) { file.Close(); return nil, err }
	if _, err = file.Write(payload); err != nil {
		return fail(err)
	}
	if err = syscall.Fchmod(fd, 0o500); err != nil {
		return fail(err)
	}
	const addSeals, sealAll = 1033, 0x1 | 0x2 | 0x4 | 0x8 // F_ADD_SEALS; SEAL, SHRINK, GROW, WRITE
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), addSeals, sealAll); errno != 0 {
		return fail(errno)
	}
	if _, err = file.Seek(0, 0); err != nil {
		return fail(err)
	}
	return file, nil
}

// unlinkedBridge validates dir as a private absolute directory of this account,
// then stages only on executable tmpfs. It never falls back to disk.
func unlinkedBridge(dir string, payload []byte) (*os.File, error) {
	info, err := os.Stat(dir)
	if err != nil || !filepath.IsAbs(dir) {
		return nil, errAppPriv
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 || !ok || int(st.Uid) != os.Geteuid() {
		return nil, errAppPriv
	}
	return memoryexec.Open(dir, payload)
}
