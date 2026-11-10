package idgateway

import (
	"context"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"log"
	"math/rand/v2"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/dsmverify"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
)

// These are retry spacing, never deadlines for slow native work. Each attempt
// may use the entire remaining operation allowance, without resetting it.
const verifyBackoff = 250 * time.Millisecond
const verifyBackoffMax = 8 * time.Second

func retryVerification(ctx context.Context, work func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, runtimebudget.Operation)
	defer cancel()
	delay := verifyBackoff
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := work(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A definitive authority answer wins even if wrapped with other evidence.
		if errors.Is(err, api.ErrNoSession) || errors.Is(err, api.ErrAdminRequired) || errors.Is(err, api.ErrAppPrivilegeDenied) || errors.Is(err, api.ErrAccountDisabled) || errors.Is(err, ErrGroupRequired) || errors.Is(err, ErrNoAccount) || errors.Is(err, errSessionChanged) {
			return err
		}
		class := dsmverify.Class(err)
		if class == "" {
			return err
		}
		// Equal jitter avoids a synchronized burst and keeps a nonzero lower bound.
		pause := delay/2 + time.Duration(rand.Int64N(int64(delay/2)))
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Only fixed fields: no errors, account names, session carriers or URLs.
		log.Printf("idgateway: DSM verification retry attempt=%d class=%s", attempt+1, class)
		delay = min(delay*2, verifyBackoffMax)
	}
}
