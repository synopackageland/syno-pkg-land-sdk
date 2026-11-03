package main

import (
	"testing"
)

func TestNoFilePolicyIsBoundedAndNeverRaisesHardLimit(t *testing.T) {
	for _, n := range []int{-1, 1, 1023, 1048577} {
		if validateNoFile(n) == nil {
			t.Fatal("accepted", n)
		}
	}
	for _, n := range []int{0, 1024, 32768, 1048576} {
		if err := validateNoFile(n); err != nil {
			t.Fatal(n, err)
		}
	}
	for _, tc := range []struct {
		cur, max uint64
		want     uint64
		bad      bool
	}{
		{4095, 4096, 0, true}, {1024, 32768, 32768, false}, {65536, 65536, 65536, false},
	} {
		got, err := noFileSoftLimit(32768, tc.cur, tc.max)
		if (err != nil) != tc.bad || !tc.bad && got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	if got, err := noFileSoftLimit(0, 1024, 4096); err != nil || got != 1024 {
		t.Fatal(got, err)
	}
}
