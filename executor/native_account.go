package main

import (
	"context"
	_ "embed"
	"errors"
	"sort"
	"unicode/utf8"
)

var errNativeAccount = errors.New("DSM account verification unavailable")
var (
	//go:embed native/account-x86_64.elf
	accountAMD64 []byte
	//go:embed native/account-aarch64.elf
	accountARM64 []byte
	//go:embed native/account-armv7.elf
	accountARM     []byte
	accountBridges = map[string][]byte{"amd64": accountAMD64, "arm64": accountARM64, "arm": accountARM}
)

func parseNativeAccount(out []byte, name string, err error) (Subject, error) {
	var s Subject
	if err != nil || !utf8.Valid(out) || strictJSON(out, &s) != nil || s.Username != name || s.UID < 1024 || uint64(s.UID) > 1<<32-2 || s.GID < 0 || uint64(s.GID) > 1<<32-2 || len(s.Groups) == 0 || len(s.Groups) > 1024 || s.Expired != "normal" {
		return Subject{}, errNativeAccount
	}
	for _, g := range s.Groups {
		if g < 0 || uint64(g) > 1<<32-2 {
			return Subject{}, errNativeAccount
		}
	}
	sort.Ints(s.Groups)
	return s, nil
}

// NativeAccount is injected by tests; production always reads through the
// pinned NSS/expiry bridge and never falls back to Core.User's missing field.
func nativeAccountReader(dir string) func(context.Context, string) (Subject, error) {
	return nativeAccountBridge{Dir: dir}.Lookup
}
