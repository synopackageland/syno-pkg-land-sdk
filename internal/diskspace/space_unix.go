//go:build linux || darwin

package diskspace

import (
	"math"
	"os"

	"golang.org/x/sys/unix"
)

// Available measures space usable by an unprivileged package account on the
// opened destination filesystem, excluding blocks reserved for root.
func Available(f *os.File) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil {
		return 0, err
	}
	block := uint64(st.Bsize)
	if block == 0 {
		return 0, os.ErrInvalid
	}
	if uint64(st.Bavail) > math.MaxInt64/block {
		return math.MaxInt64, nil
	}
	return int64(uint64(st.Bavail) * block), nil
}
