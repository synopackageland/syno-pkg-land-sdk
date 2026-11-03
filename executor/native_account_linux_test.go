//go:build linux

package main

import (
	"context"
	"testing"
	"time"
)

func TestNativeAccountBridgeDeadlineAndSource(t *testing.T) {
	raw := `{"username":"alice","uid":1200,"gid":100,"groups":[100],"expired":"normal","admin":true,"home":"/home/alice","source":"local"}`
	b := nativeAccountBridge{Dir: t.TempDir(), payload: []byte("#!/bin/sh\nsleep 3.1\nprintf '%s\\n' '" + raw + "'\n")}
	// A healthy controller can exceed the old three-second cutoff.
	s, err := b.Lookup(context.Background(), "alice")
	if err != nil || s.Username != "alice" || !s.Admin {
		t.Fatal(s, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if s, err := b.Lookup(ctx, "alice"); err == nil || s.Username != "" || time.Since(start) > 2*time.Second {
		t.Fatal("caller deadline did not refuse promptly:", s, err)
	}
}
