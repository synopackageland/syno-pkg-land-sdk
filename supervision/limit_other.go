//go:build !linux

package supervision

import "context"

var starts = NewLimiter(2)

func AcquireStart(ctx context.Context) (func(), error) { return starts.Acquire(ctx) }
