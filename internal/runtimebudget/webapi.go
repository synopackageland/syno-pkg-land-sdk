package runtimebudget

import "time"

const MaxWebAPIRawBytes int64 = 10 << 30
const WebAPIRawTimeout = 3 * time.Hour

// WebAPIRawForSize allows about 1 MiB/s, with time for setup on small files.
// A negative size means unknown, so only the overall ceiling can be applied.
func WebAPIRawForSize(size int64) time.Duration {
	if size < 0 || size > MaxWebAPIRawBytes {
		return WebAPIRawTimeout
	}
	gib := (size + (1 << 30) - 1) >> 30
	return min(WebAPIRawTimeout, max(30*time.Minute, time.Duration(gib)*18*time.Minute))
}
