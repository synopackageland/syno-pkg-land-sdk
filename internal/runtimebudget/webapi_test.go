package runtimebudget

import (
	"testing"
	"time"
)

func TestWebAPIRawForSize(t *testing.T) {
	for _, tt := range []struct {
		size int64
		want time.Duration
	}{
		{-1, Operation},
		{0, Operation},
		{1 << 30, Operation},
		{(1 << 30) + 1, Operation},
		{5 << 30, Operation},
		{10 << 30, Operation},
		{(10 << 30) + 1, Operation},
		{1<<63 - 1, Operation},
	} {
		if got := WebAPIRawForSize(tt.size); got != tt.want {
			t.Errorf("size %d: got %s, want %s", tt.size, got, tt.want)
		}
	}
}
