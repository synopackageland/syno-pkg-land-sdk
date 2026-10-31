//go:build !linux

package supervision

import "context"

func WatchNetwork(ctx context.Context, changed func()) error { <-ctx.Done(); return ctx.Err() }
