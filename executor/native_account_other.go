//go:build !linux

package main

import "context"

type nativeAccountBridge struct{ Dir string }

func preparedNativeAccountBridge(dir string) nativeAccountBridge {
	return nativeAccountBridge{Dir: dir}
}

func (nativeAccountBridge) Lookup(context.Context, string) (Subject, error) {
	return Subject{}, errNativeAccount
}
