//go:build linux

package main

import (
	"context"
	"os"
	"runtime"
	"time"
)

type nativeAccountBridge struct {
	Dir     string
	payload []byte
}

func (b nativeAccountBridge) Lookup(ctx context.Context, name string) (Subject, error) {
	payload := b.payload
	if payload == nil {
		payload = accountBridges[runtime.GOARCH]
	}
	if len(payload) == 0 {
		return Subject{}, errNativeAccount
	}
	f, err := (appPrivBridge{Dir: b.Dir}).stage(payload)
	if err != nil {
		return Subject{}, errNativeAccount
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := command(ctx, "/proc/self/fd/3", []string{name})
	cmd.ExtraFiles = []*os.File{f}
	out := &boundedOutput{limit: 64 << 10}
	cmd.Stdout = out
	err = cmd.Run()
	return parseNativeAccount(out.buf, name, err)
}
