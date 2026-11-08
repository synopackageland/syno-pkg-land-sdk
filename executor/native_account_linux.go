//go:build linux

package main

import (
	"context"
	"os"
	"runtime"

	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/budget"
)

type nativeAccountBridge struct {
	Dir     string
	payload []byte
	bridge  *appPrivBridge
}

func preparedNativeAccountBridge(dir string) nativeAccountBridge {
	bridge := (appPrivBridge{Dir: dir}).prepare(accountBridges[runtime.GOARCH])
	return nativeAccountBridge{Dir: dir, bridge: &bridge}
}

func (b nativeAccountBridge) Lookup(ctx context.Context, name string) (Subject, error) {
	payload := b.payload
	if payload == nil {
		payload = accountBridges[runtime.GOARCH]
	}
	if len(payload) == 0 {
		return Subject{}, errNativeAccount
	}
	bridge := appPrivBridge{Dir: b.Dir}
	if b.bridge != nil {
		bridge = *b.bridge
	}
	f, err := bridge.stage(payload)
	if err != nil {
		return Subject{}, errNativeAccount
	}
	defer f.Close()
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		// Native directory work shares the operation default; explicit caller
		// deadlines and cancellation retain their original authority.
		ctx, cancel = context.WithTimeout(ctx, budget.Operation)
		defer cancel()
	}
	cmd := command(ctx, "/proc/self/fd/3", []string{name})
	cmd.ExtraFiles = []*os.File{f}
	out := &boundedOutput{limit: 64 << 10}
	cmd.Stdout = out
	err = cmd.Run()
	return parseNativeAccount(out.buf, name, err)
}
