//go:build !unix

package datamigrate

import "context"

// lock is a no-op where DSM never runs a package; Run still re-reads the state
// before it applies anything.
func lock(context.Context, string) (func(), error) { return func() {}, nil }
