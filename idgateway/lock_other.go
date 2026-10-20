//go:build !unix

package idgateway

// lockFile is a no-op where DSM never runs the gateway.
func lockFile(string) (func(), error) { return func() {}, nil }
