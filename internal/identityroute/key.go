// Package identityroute restricts public ceremony markers to SDK assembly.
package identityroute

// Key cannot be issued by an App outside the SDK's internal import boundary.
type Key struct{ ceremony bool }

func New() Key            { return Key{ceremony: true} }
func (k Key) Valid() bool { return k.ceremony }
