package budget

import (
	"testing"
	"time"
)

func TestWebAPIRawSizeBudgets(t *testing.T) {
	for _, tt := range []struct {
		size int64
		want time.Duration
	}{
		{-1, 3 * time.Hour},
		{0, 30 * time.Minute},
		{1 << 30, 30 * time.Minute},
		{(1 << 30) + 1, 36 * time.Minute},
		{5 << 30, 90 * time.Minute},
		{10 << 30, 3 * time.Hour},
		{(10 << 30) + 1, 3 * time.Hour},
		{1<<63 - 1, 3 * time.Hour},
	} {
		if got := WebAPIRawForSize(tt.size); got != tt.want {
			t.Errorf("size %d: got %s, want %s", tt.size, got, tt.want)
		}
	}
}
