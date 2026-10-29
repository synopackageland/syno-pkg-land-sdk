//go:build !linux

package ipc

import (
	"context"
	"errors"
	"net"
)

func startPeerUnix(context.Context, string, string, PeerPolicy, func(context.Context, net.Conn, PeerCredentials)) (*PeerServer, error) {
	return nil, errors.New("ipc: peer credentials require Linux")
}

func dialPeerUnix(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("ipc: peer credentials require Linux")
}
