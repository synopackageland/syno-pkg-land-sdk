//go:build !linux

package idgateway

import (
	"context"
	"errors"
)

// watchFile needs Linux inotify; elsewhere only the startup comparison runs.
func watchFile(context.Context, string, string, func()) error {
	return errors.New("watching the passwd file needs Linux inotify")
}
