//go:build !linux

package main

import "context"

type nativeAccountBridge struct{ Dir string }

func (nativeAccountBridge) Lookup(context.Context, string) (Subject, error) {
	return Subject{}, errNativeAccount
}
