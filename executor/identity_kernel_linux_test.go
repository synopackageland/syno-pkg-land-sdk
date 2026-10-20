//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
)

func TestSealedIdentityRealKernel(t *testing.T) {
	if mode := os.Getenv("T0188_IDENTITY_CHILD"); mode != "" {
		runtime.LockOSThread()
		subject := Subject{UID: 1200, GID: 100, Groups: []int{100}}
		var err error
		if mode == "unsealed" {
			// Clear capabilities and IDs without setting no_new_privs.
			// VerifyPrivileges must still refuse this otherwise sealed child.
			for cap := uintptr(0); cap < 64; cap++ {
				_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 24, cap, 0, 0, 0, 0)
				if errno != 0 && errno != syscall.EINVAL {
					fmt.Fprintln(os.Stderr, errno)
					os.Exit(1)
				}
			}
			err = dropIdentity(realIdentity{}, subject)
			if err == nil {
				err = realIdentity{}.VerifyPrivileges(subject)
			}
			if err == nil || err.Error() != "no_new_privs missing" {
				fmt.Fprintln(os.Stderr, "expected no_new_privs refusal, got", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
		err = dropSealedIdentity(realIdentity{}, subject)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Geteuid() != 0 {
		t.Skip("root Linux fixture required")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"sealed", "unsealed"} {
		cmd := exec.Command(exe, "-test.run=^TestSealedIdentityRealKernel$")
		cmd.Env = append(os.Environ(), "T0188_IDENTITY_CHILD="+mode)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s subject kernel verification: %s: %v", mode, out, err)
		}
	}
}
