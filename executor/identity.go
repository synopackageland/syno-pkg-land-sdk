package main

import (
	"errors"
	"fmt"
	"sort"
)

// Injectable syscall surface: tests verify the exact ordering and every error.
type identitySwitch interface {
	Setgroups([]int) error
	Setresgid(int, int, int) error
	Setresuid(int, int, int) error
	IDs() (int, int, int, int)
	Groups() ([]int, error)
}

func dropIdentity(x identitySwitch, s Subject) error {
	if s.UID < 1024 || s.GID < 0 || len(s.Groups) == 0 {
		return errors.New("subject")
	}
	for _, g := range s.Groups {
		if g < 0 {
			return errors.New("groups")
		}
	}
	if err := x.Setgroups(s.Groups); err != nil {
		return err
	}
	if err := x.Setresgid(s.GID, s.GID, s.GID); err != nil {
		return err
	}
	if err := x.Setresuid(s.UID, s.UID, s.UID); err != nil {
		return err
	}
	uid, euid, gid, egid := x.IDs()
	gs, err := x.Groups()
	if err != nil {
		return err
	}
	want := append([]int(nil), s.Groups...)
	sort.Ints(want)
	sort.Ints(gs)
	if uid != s.UID || euid != s.UID || gid != s.GID || egid != s.GID || fmt.Sprint(gs) != fmt.Sprint(want) {
		return errors.New("identity mismatch")
	}
	return nil
}

func sameGroups(a, b []int) bool {
	a = append([]int(nil), a...)
	b = append([]int(nil), b...)
	sort.Ints(a)
	sort.Ints(b)
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// Keep the historical injectable identity interface for existing consumer
// fixtures. Production fixed children require this stricter syscall surface.
type sealedIdentitySwitch interface {
	identitySwitch
	Seal() error
	VerifyPrivileges(Subject) error
}

func dropSealedIdentity(x sealedIdentitySwitch, s Subject) error {
	if err := x.Seal(); err != nil {
		return err
	}
	if err := dropIdentity(x, s); err != nil {
		return err
	}
	return x.VerifyPrivileges(s)
}
