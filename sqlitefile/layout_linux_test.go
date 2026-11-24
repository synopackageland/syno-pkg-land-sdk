//go:build linux

package sqlitefile

import (
	"errors"
	"testing"
)

func TestPrepareFlags(t *testing.T) {
	probeErr := errors.New("compression probe")
	for _, tt := range []struct {
		name     string
		flags    int
		property bool
		probeErr error
		wantSet  bool
	}{
		{name: "preserves existing flags", flags: 0x80, wantSet: true},
		{name: "already enabled", flags: fsNoCOW},
		{name: "compression flag", flags: fsCompress},
		{name: "compression property", property: true},
		{name: "unknown compression", probeErr: probeErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			err := prepareFlags(tt.flags, func() (bool, error) { return tt.property, tt.probeErr }, func(flags int) error {
				called = true
				if flags != tt.flags|fsNoCOW {
					t.Fatalf("set flags %#x from %#x", flags, tt.flags)
				}
				return nil
			})
			if called != tt.wantSet || !errors.Is(err, tt.probeErr) {
				t.Fatalf("set=%v err=%v, want set=%v err=%v", called, err, tt.wantSet, tt.probeErr)
			}
		})
	}
}
