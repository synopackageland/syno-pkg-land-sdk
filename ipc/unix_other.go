//go:build !linux

package ipc

import (
	"context"
	"errors"
	"net"
	"net/http"
)

func serveUnix(context.Context, string, string, http.Handler) error {
	return errors.New("ipc: private Unix HTTP requires Linux")
}

func startUnix(context.Context, string, string, http.Handler) (*Server, error) {
	return nil, errors.New("ipc: private Unix HTTP requires Linux")
}

func dialUnix(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("ipc: private Unix HTTP requires Linux")
}
