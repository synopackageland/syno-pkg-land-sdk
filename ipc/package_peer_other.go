//go:build !linux

package ipc

import (
	"context"
	"errors"
	"net"
)

func startPackagePeerUnix(context.Context, string, PeerAuthorizer, func(context.Context, net.Conn, PeerCredentials)) (*PeerServer, error) {
	return nil, errors.New("ipc: package peer credentials require Linux")
}
