package budget

import "time"

// Keep the standalone executor build independent of unreleased SDK helpers.
// These bounds implement docs/webapi-raw.md, as does the Go client's budget.
const MaxWebAPIRawBytes int64 = 10 << 30
const WebAPIRawTimeout = 3 * time.Hour

func WebAPIRawForSize(size int64) time.Duration {
	if size < 0 || size > MaxWebAPIRawBytes {
		return WebAPIRawTimeout
	}
	gib := (size + (1 << 30) - 1) >> 30
	return min(WebAPIRawTimeout, max(30*time.Minute, time.Duration(gib)*18*time.Minute))
}
