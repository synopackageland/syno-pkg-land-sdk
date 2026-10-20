package portal

import (
	"context"
	"errors"
)

// The executor's Application Portal entry. An App whose os_min_ver is below
// 7.2 cannot declare DSM's web-config resource (DSM 7.1 refuses it for
// unsigned packages), so its executor registers the entry instead, with the
// portal.register and portal.unregister ops of an approved policy.
const (
	EntryRegistered      = "registered"
	EntryUnregistered    = "unregistered"
	EntryCleanupRequired = "cleanup-required"
)

// Executor is the executor client; *delegate.Client satisfies it.
type Executor interface {
	Call(ctx context.Context, op string, auth map[string]string, params, result any) error
}

var errEntry = errors.New("portal: unknown entry state")

func knownEntry(entry string) bool {
	return entry == EntryRegistered || entry == EntryUnregistered || entry == EntryCleanupRequired
}

// Register asks the executor to register the entry. auth is the caller's
// DSM session (api.DSMSession); the executor requires an administrator.
// Executor refusals, such as PORTAL_FAILED, are returned unchanged.
func Register(ctx context.Context, c Executor, auth map[string]string) (string, error) {
	return change(ctx, c, "portal.register", auth)
}

// Unregister asks the executor to remove the entry, its saved alias and the
// nginx configuration DSM generated for it.
func Unregister(ctx context.Context, c Executor, auth map[string]string) (string, error) {
	return change(ctx, c, "portal.unregister", auth)
}

func change(ctx context.Context, c Executor, op string, auth map[string]string) (string, error) {
	var out struct {
		Entry string `json:"entry"`
	}
	if err := c.Call(ctx, op, auth, struct{}{}, &out); err != nil {
		return "", err
	}
	if !knownEntry(out.Entry) {
		return "", errEntry
	}
	return out.Entry, nil
}

// ExecutorEntry reads the entry from the executor's status.
func ExecutorEntry(ctx context.Context, c Executor) (string, error) {
	var out struct {
		Portal string `json:"portal"`
	}
	if err := c.Call(ctx, "status", nil, struct{}{}, &out); err != nil {
		return "", err
	}
	if !knownEntry(out.Portal) {
		return "", errEntry
	}
	return out.Portal, nil
}

// WithEntry is s, read from DSM's saved setting, for an App whose executor
// registers its entry: without the entry DSM serves no alias.
func WithEntry(s Status, entry string) Status {
	switch entry {
	case EntryRegistered, EntryCleanupRequired:
		s.Entry = entry
		return s
	case EntryUnregistered:
		return Status{State: Inactive, Entry: entry}
	}
	return Status{State: Unavailable}
}
