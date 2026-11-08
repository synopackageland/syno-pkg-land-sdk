//go:build linux

package dsm

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"strconv"
	"sync"

	"golang.org/x/sys/unix"
)

var bridgeFallbackCache struct {
	sync.Mutex
	dir   string
	files [3]*os.File
}

func bridgePayloads() [3][]byte {
	return [3][]byte{adminBridges[runtime.GOARCH], appPrivBridges[runtime.GOARCH], accountBridges[runtime.GOARCH]}
}

// SetBridgeFallbackDir is startup-only. Cache at most the three embedded native
// bridges before serving requests. Failed preparation never writes on a later
// check; account/role/privilege verification still fails closed.
func prepareBridgeFallback(dir string) {
	bridgeFallbackCache.Lock()
	defer bridgeFallbackCache.Unlock()
	for i, f := range bridgeFallbackCache.files {
		if f != nil {
			f.Close()
		}
		bridgeFallbackCache.files[i] = nil
	}
	bridgeFallbackCache.dir = dir
	fd, err := memfdCreate("synopkgland-probe", unix.MFD_CLOEXEC)
	if err == nil {
		unix.Close(fd)
		return
	}
	if !errors.Is(err, unix.ENOSYS) {
		return
	}
	for i, payload := range bridgePayloads() {
		if len(payload) > 0 {
			bridgeFallbackCache.files[i], _ = unlinkedBridge(dir, payload)
		}
	}
}

func cachedBridgeFallback(payload []byte) (*os.File, error) {
	for i, native := range bridgePayloads() {
		if len(native) == 0 || !bytes.Equal(native, payload) {
			continue
		}
		bridgeFallbackCache.Lock()
		defer bridgeFallbackCache.Unlock()
		f := bridgeFallbackCache.files[i]
		if f == nil || bridgeFallbackCache.dir != bridgeFallbackDir {
			return nil, errBridgeDir
		}
		// Reopen instead of dup: independent file offsets and read-only access.
		return os.Open("/proc/self/fd/" + strconv.FormatUint(uint64(f.Fd()), 10))
	}
	// Non-embedded payloads are used only by bridge transport fixtures.
	return unlinkedBridge(bridgeFallbackDir, payload)
}
