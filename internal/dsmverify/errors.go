// Package dsmverify classifies temporary failures of read-only DSM checks.
package dsmverify

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
)

// Class returns a fixed, credential-free retry class. Unknown failures stay
// final: error strings, command output and authorization answers are not codes.
func Class(err error) string {
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED), errors.Is(err, syscall.EPIPE):
		return "connection"
	case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EBUSY), errors.Is(err, syscall.ENOMEM), errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE), errors.Is(err, syscall.EINTR):
		return "busy"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "truncated"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return ""
}
