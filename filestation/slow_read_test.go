package filestation

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

type slowSession struct{ DSMClient }

func (slowSession) Verify(ctx context.Context, _ *http.Request, _ string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(60 * time.Second):
		return nil
	}
}

func TestSlowDSMVerificationAndCancellation(t *testing.T) {
	c := callerFor(t, "reader")
	for _, canceled := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				go func() { time.Sleep(time.Second); cancel() }()
			}
			err := NewService(slowSession{}).verify(ctx, c, sessionRequest())
			if canceled && err == nil || !canceled && err != nil {
				t.Fatalf("canceled=%v: %v", canceled, err)
			}
		})
	}
}
