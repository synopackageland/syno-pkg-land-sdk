package delegate

import (
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"testing"
)

func TestReviewBusyJournalHasRetryableStableCode(t *testing.T) {
	var out *api.Error
	if !errors.As(jobError(background.ErrBusy), &out) || out.Code != "jobs_busy" {
		t.Fatalf("bounded lock timeout mapped to generic error: %+v", out)
	}
}
