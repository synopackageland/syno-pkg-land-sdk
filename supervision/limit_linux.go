//go:build linux

package supervision

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"
)

// AcquireStart reserves one of two kernel-owned host slots. Abstract Unix
// sockets cross package UIDs, need no filesystem permissions or service, and
// vanish on close/crash. This is load coordination, not an authorization boundary.
// Hold the lease through startup stabilization, never over backoff or lifetime.
func AcquireStart(ctx context.Context, waiting ...func()) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for i := 0; i < 2; i++ {
			ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: fmt.Sprintf("@synopkgland-start-v1-%d", i), Net: "unix"})
			if err == nil {
				return func() { ln.Close() }, nil
			}
			if !errors.Is(err, syscall.EADDRINUSE) {
				return nil, err
			}
		}
		if len(waiting) > 0 && waiting[0] != nil {
			waiting[0]()
			waiting = nil
		}
		if err := Wait(ctx, 250*time.Millisecond); err != nil {
			return nil, err
		}
	}
}
