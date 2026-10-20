//go:build !linux

package main

import "context"

// DSM's native SDK requires Linux; host tests inject a decision.
type appPrivBridge struct{ Dir string }

func (appPrivBridge) Check(context.Context, string, string, string) (bool, error) {
	return false, errAppPriv
}
