// Package runtimebudget derives work deadlines without changing authority lifetimes.
package runtimebudget

import (
	"context"
	"time"
)

const Operation = 60 * time.Second
const Cleanup = 5 * time.Second
const MaxSeconds = 3600

// Inner leaves a bounded response/cleanup reserve inside an outer deadline.
func Inner(remaining time.Duration) time.Duration {
	return max(0, remaining-min(Cleanup, remaining/4))
}

func Remaining(ctx context.Context, fallback time.Duration) time.Duration {
	if until, ok := ctx.Deadline(); ok {
		return max(0, time.Until(until))
	}
	return fallback
}
