package main

import (
	_ "embed"
	"errors"
)

// DSM's SLIBAppPrivUserHas bridge per runtime.GOARCH: the CGI's
// ../dsm/native/apppriv-*.elf, built reproducibly from apppriv_check.c there.
// Embedding binds it to the executor's digest; nothing is read at run time.
var (
	//go:embed native/apppriv-x86_64.elf
	appPrivAMD64 []byte
	//go:embed native/apppriv-aarch64.elf
	appPrivARM64 []byte
	//go:embed native/apppriv-armv7.elf
	appPrivARMv7 []byte

	appPrivBridges = map[string][]byte{"amd64": appPrivAMD64, "arm64": appPrivARM64, "arm": appPrivARMv7}
)

var errAppPriv = errors.New("DSM Application Privileges verification unavailable")

// parseAppPriv accepts only the bridge's exact "1\n" or "0\n".
func parseAppPriv(out []byte, err error) (bool, error) {
	if err != nil {
		return false, errAppPriv
	}
	switch string(out) {
	case "1\n":
		return true, nil
	case "0\n":
		return false, nil
	}
	return false, errAppPriv
}
