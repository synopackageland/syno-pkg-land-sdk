//go:build unix

package runtimebudget

import (
	"golang.org/x/sys/unix"
	"strconv"
	"time"
)

func monotonic() (time.Duration, error) {
	var t unix.Timespec
	err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &t)
	return time.Duration(t.Sec)*time.Second + time.Duration(t.Nsec), err
}

// EncodeDeadline transfers a kernel monotonic deadline across exec, including
// time spent scheduling and loading the child. Wall clock changes cannot reset it.
func EncodeDeadline(until time.Time) (string, error) {
	now, err := monotonic()
	return strconv.FormatInt(int64(now+time.Until(until)), 10), err
}
func DecodeDeadline(token string) (time.Time, error) {
	n, err := strconv.ParseInt(token, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	wall := time.Now()
	now, err := monotonic()
	return wall.Add(time.Duration(n) - now), err
}
