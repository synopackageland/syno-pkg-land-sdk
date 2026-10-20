package main

import (
	"errors"
	"testing"
)

type secureSwitch struct {
	sealed, verified bool
	bad              bool
	subject          Subject
}

func (s *secureSwitch) Seal() error { s.sealed = true; return nil }
func (s *secureSwitch) VerifyPrivileges(Subject) error {
	s.verified = true
	if s.bad {
		return errors.New("saved root or capability")
	}
	return nil
}
func (s *secureSwitch) Setgroups([]int) error {
	if !s.sealed {
		return errors.New("not sealed")
	}
	return nil
}
func (s *secureSwitch) Setresgid(int, int, int) error { return nil }
func (s *secureSwitch) Setresuid(int, int, int) error { return nil }
func (s *secureSwitch) IDs() (int, int, int, int) {
	return s.subject.UID, s.subject.UID, s.subject.GID, s.subject.GID
}
func (s *secureSwitch) Groups() ([]int, error) { return s.subject.Groups, nil }
func TestDropAdminSealsAndChecksSavedIDsCapabilities(t *testing.T) {
	subject := Subject{UID: 1200, GID: 100, Groups: []int{100, 101}, Admin: true}
	for _, bad := range []bool{false, true} {
		s := &secureSwitch{bad: bad, subject: subject}
		err := dropSealedIdentity(s, subject)
		if (err != nil) != bad || !s.verified {
			t.Fatal("unverified privilege drop", err)
		}
	}
}
