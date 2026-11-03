package budget

import "time"

// Keep the standalone executor build independent of unreleased SDK helpers.
// These bounds implement docs/webapi-raw.md, as does the Go client's budget.
const MaxWebAPIRawBytes int64 = 10 << 30
const WebAPIRawTimeout = Operation

func WebAPIRawForSize(size int64) time.Duration {
	return WebAPIRawTimeout
}
