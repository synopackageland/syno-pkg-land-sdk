package supervision

import (
	"context"
	"time"
)

// Wait is a cancellable one-shot wait; it retains no timer after returning.
func Wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(max(0, d))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return ctx.Err()
	}
}

// Dependency waits without a total deadline. Each probe must honor its context.
// Transient absence is a waiting state and does not spend a restart attempt.
func Dependency(ctx context.Context, probe func(context.Context) error, waiting func()) error {
	if waiting != nil && ctx.Err() == nil {
		waiting()
		waiting = nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := probe(bounded)
		cancel()
		if err == nil {
			return nil
		}
		if err := Wait(ctx, 2*time.Second); err != nil {
			return err
		}
	}
}

// Limiter bounds concurrent starts within an embedding process. The host
// limiter additionally coordinates independent package supervisors on Linux.
type Limiter struct{ slots chan struct{} }

func NewLimiter(n int) *Limiter {
	if n < 1 {
		panic("positive start limit required")
	}
	return &Limiter{make(chan struct{}, n)}
}
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	select {
	case l.slots <- struct{}{}:
		return func() { <-l.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
