//go:build linux

package supervision

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
)

// NetworkReady checks a default route and resolves the actual dependency names.
// It does not ping an unrelated public service or change routes/resolver state.
func NetworkReady(ctx context.Context, hosts ...string) error {
	route := false
	if raw, err := os.ReadFile("/proc/net/route"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			f := strings.Fields(line)
			if len(f) > 3 && f[1] == "00000000" && routeFlags(f[3]) {
				route = true
			}
		}
	}
	if raw, err := os.ReadFile("/proc/net/ipv6_route"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			f := strings.Fields(line)
			if len(f) > 9 && f[0] == strings.Repeat("0", 32) && f[1] == "00" && f[9] != "lo" && routeFlags(f[8]) {
				route = true
			}
		}
	}
	if !route {
		return errors.New("waiting for default route")
	}
	for _, host := range hosts {
		if _, err := net.DefaultResolver.LookupIPAddr(ctx, host); err != nil {
			return errors.New("waiting for DNS")
		}
	}
	return nil
}

func routeFlags(raw string) bool {
	flags, err := strconv.ParseUint(raw, 16, 32)
	return err == nil && flags&1 != 0 && flags&0x200 == 0
}
