//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func (realIdentity) Seal() error {
	// These are per-thread Linux settings. Every fresh child locks its OS thread
	// until exit; it never execs untrusted code after dropping the identity.
	prctl := func(op, a uintptr) error {
		_, _, err := syscall.Syscall6(syscall.SYS_PRCTL, op, a, 0, 0, 0, 0)
		if err != 0 {
			return err
		}
		return nil
	}
	if err := prctl(38, 1); err != nil {
		return err
	} // PR_SET_NO_NEW_PRIVS
	if err := prctl(8, 0); err != nil {
		return err
	} // PR_SET_KEEPCAPS off
	for cap := uintptr(0); cap < 64; cap++ {
		if err := prctl(24, cap); err != nil && err != syscall.EINVAL {
			return err
		}
	} // PR_CAPBSET_DROP
	return nil
}
func (realIdentity) VerifyPrivileges(s Subject) error {
	b, err := os.ReadFile(fmt.Sprintf("/proc/self/task/%d/status", syscall.Gettid()))
	if err != nil {
		return err
	}
	fields := map[string][]string{}
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.Fields(line)
		if len(parts) > 1 {
			fields[strings.TrimSuffix(parts[0], ":")] = parts[1:]
		}
	}
	for key, want := range map[string]int{"Uid": s.UID, "Gid": s.GID} {
		got := fields[key]
		if len(got) != 4 {
			return errors.New("saved IDs missing")
		}
		for _, v := range got {
			n, err := strconv.Atoi(v)
			if err != nil || n != want {
				return errors.New("saved IDs mismatch")
			}
		}
	}
	for _, key := range []string{"CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"} {
		v := fields[key]
		if len(v) != 1 {
			return errors.New("capability missing")
		}
		n, err := strconv.ParseUint(v[0], 16, 64)
		if err != nil || n != 0 {
			return errors.New("capability retained")
		}
	}
	// Linux 4.4 supports no_new_privs but does not expose NoNewPrivs in
	// proc status. Query the locked writer thread directly; missing/failed
	// kernel support still refuses the write, without relaxing the seal.
	nnp, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 39, 0, 0, 0, 0, 0) // PR_GET_NO_NEW_PRIVS
	if errno != 0 {
		return errno
	}
	if nnp != 1 {
		return errors.New("no_new_privs missing")
	}
	return nil
}
