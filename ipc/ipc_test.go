package ipc

import (
	"context"
	"testing"
)

func TestDialPackageUnixRefusesOtherPaths(t *testing.T) {
	for _, tc := range [][2]string{
		{"App", "/var/packages/Other/var/run/a.sock"},
		{"App", "/var/packages/App/var/run/d/a.sock"},
		{"App", "/var/packages/App/var/run/a"},
		{"App", "relative.sock"},
		{"", "/var/packages/App/var/run/a.sock"},
	} {
		if _, err := DialPackageUnix(context.Background(), tc[0], tc[1]); err == nil || ValidPackageSocket(tc[0], tc[1]) == nil {
			t.Errorf("%v accepted", tc)
		}
	}
	if err := ValidPackageSocket("App", "/var/packages/App/var/run/a.sock"); err != nil {
		t.Fatal(err)
	}
}
