package runtimebudget

import "time"

const MaxWebAPIRawBytes int64 = 10 << 30
const WebAPIRawTimeout = Operation

// WebAPIRawForSize retains the API but uses the shared operation allowance.
// Byte size affects validation, never the default deadline.
func WebAPIRawForSize(size int64) time.Duration {
	return WebAPIRawTimeout
}
