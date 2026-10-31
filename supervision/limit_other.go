//go:build !linux

package supervision

import "context"

var starts = NewLimiter(2)

func AcquireStart(ctx context.Context, waiting ...func()) (func(), error) {
	if len(starts.slots) == cap(starts.slots) && len(waiting) > 0 && waiting[0] != nil {
		waiting[0]()
	}
	return starts.Acquire(ctx)
}
