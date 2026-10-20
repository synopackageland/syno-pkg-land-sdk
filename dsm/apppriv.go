package dsm

import (
	"context"
	"errors"
	"net/netip"
	"regexp"
	"time"
)

var errNativeAppPriv = errors.New("DSM Application Privileges verification unavailable")

// appID is a DSM application ID such as SYNO.SDS.<package>.Instance.
var appID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(\.[A-Za-z][A-Za-z0-9]*){1,7}$`)

// AppPrivilege asks DSM's native SDK whether principal may use app from addr.
// DSM applies its own user, group and everyone rules, deny precedence and IP
// ranges, and always allows administrators. Unknown results refuse.
func AppPrivilege(ctx context.Context, principal, app, addr string) (bool, error) {
	if !validPrincipal(principal) || len(app) > 128 || !appID.MatchString(app) {
		return false, errNativeAppPriv
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil || ip.Zone() != "" || ip.String() != addr {
		return false, errNativeAppPriv
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return nativeAppPrivilege(ctx, principal, app, addr)
}
