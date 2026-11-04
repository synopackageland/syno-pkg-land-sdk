package dsm

import (
	"context"
	"net/http"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// NewMediaAuthenticator returns an uncached DSM entry authenticator for media.
// The media service supplies the request's sealed token; this authenticator
// still verifies the live DSM session, enabled account and App Privileges.
func NewMediaAuthenticator(mode, packageID string) (api.EntryAuthenticator, error) {
	gate, err := NewEntryGate(NewAuthenticator(), mode, packageID, "")
	if err != nil {
		return nil, err
	}
	g, ok := gate.(*EntryGate)
	if !ok {
		return nil, api.BadRequest("media requires an explicit App entry mode")
	}
	return &mediaAuthenticator{gate: g, lookup: LookupAccount}, nil
}

type mediaAuthenticator struct {
	gate   *EntryGate
	lookup func(context.Context, string) (AccountInfo, error)
}

func (a *mediaAuthenticator) EntryMode() string { return a.gate.mode }
func (a *mediaAuthenticator) Authenticate(r *http.Request) (api.Identity, error) {
	if _, err := api.DSMSession(r); err != nil {
		return api.Identity{}, err
	}
	id, err := a.gate.next.Authenticate(r)
	if err != nil {
		return api.Identity{}, err
	}
	account, err := a.lookup(r.Context(), id.User)
	if err != nil {
		return api.Identity{}, err
	}
	if account.Expired != "normal" {
		return api.Identity{}, api.ErrNoSession
	}
	if a.gate.mode == EntryAdminOnly {
		if !id.Admin {
			return api.Identity{}, api.ErrAdminRequired
		}
		return id, nil
	}
	addr, err := api.ClientAddress(r)
	if err != nil {
		return api.Identity{}, err
	}
	allowed, err := a.gate.allowed(r.Context(), id.User, a.gate.app, addr)
	if err != nil {
		return api.Identity{}, err
	}
	if !allowed {
		return api.Identity{}, api.ErrAppPrivilegeDenied
	}
	return id, nil
}
