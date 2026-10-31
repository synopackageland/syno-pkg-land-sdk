//go:build !linux

package supervision

import (
	"context"
	"net"
)

func NetworkReady(ctx context.Context, hosts ...string) error {
	for _, host := range hosts {
		if _, err := net.DefaultResolver.LookupIPAddr(ctx, host); err != nil {
			return err
		}
	}
	return nil
}
