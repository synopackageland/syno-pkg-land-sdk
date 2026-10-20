//go:build !linux

package dsm

import (
	"context"
	"os/exec"
)

// Host tests inject a role provider; DSM native roles require Linux.
func nativeAdministrator(context.Context, string) (bool, error) { return false, errNativeRole }
func nativeAppPrivilege(context.Context, string, string, string) (bool, error) {
	return false, errNativeAppPriv
}
func configureCommand(*exec.Cmd) {}
