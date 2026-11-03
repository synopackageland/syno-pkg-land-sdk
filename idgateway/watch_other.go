//go:build !linux

package idgateway

import (
	"context"
	"errors"
)

// watchFile needs Linux inotify; elsewhere only the startup comparison runs.
func watchFile(_ context.Context, _, _ string, _ func(), initial func()) error {
	if initial != nil {
		initial()
	}
	return errors.New("watching the passwd file needs Linux inotify")
}
