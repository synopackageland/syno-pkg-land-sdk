package compose

import (
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/startupbudget"
)

// applyHealthFloor keeps the App's or image's health command and only relaxes
// timing that is stricter than a slow NAS can meet. interval is left alone:
// Container Manager's Docker 24 has no start_interval, so a longer interval
// would only delay depends_on service_healthy. Without a declared command,
// Docker inherits the image HEALTHCHECK and these timing fields; an image without
// one still has no healthcheck.
func applyHealthFloor(s *types.ServiceConfig, startup int) {
	if s.HealthCheck == nil {
		s.HealthCheck = &types.HealthCheckConfig{}
	}
	h := s.HealthCheck
	if h.Disable || (len(h.Test) > 0 && h.Test[0] == "NONE") {
		return
	}
	atLeast := func(d **types.Duration, floor time.Duration) {
		if *d == nil || time.Duration(**d) < floor {
			v := types.Duration(floor)
			*d = &v
		}
	}
	atLeast(&h.Timeout, startupbudget.ProbeSeconds*time.Second)
	atLeast(&h.StartPeriod, time.Duration(startup)*time.Second)
	if h.Retries == nil || *h.Retries < startupbudget.HealthRetries {
		v := uint64(startupbudget.HealthRetries)
		h.Retries = &v
	}
}
