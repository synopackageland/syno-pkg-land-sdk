//go:build !linux

package memoryexec

import (
	"errors"
	"os"
)

var ErrUnavailable = errors.New("executable memory filesystem unavailable")

func Open(string, []byte) (*os.File, error) { return nil, ErrUnavailable }
