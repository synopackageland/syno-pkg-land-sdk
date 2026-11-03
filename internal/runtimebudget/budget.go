// Package runtimebudget derives work deadlines without changing authority lifetimes.
package runtimebudget

import (
	"context"
	"time"
)

const Operation = 5 * time.Minute

// Cleanup is work too; only the response reserve is short.
const Cleanup = Operation
const ResponseReserve = 5 * time.Second
const MaxSeconds = 3600

// Inner leaves a bounded response/cleanup reserve inside an outer deadline.
func Inner(remaining time.Duration) time.Duration {
	return max(0, remaining-min(ResponseReserve, remaining/4))
}

func Remaining(ctx context.Context, fallback time.Duration) time.Duration {
	if until, ok := ctx.Deadline(); ok {
		return max(0, time.Until(until))
	}
	return fallback
}
