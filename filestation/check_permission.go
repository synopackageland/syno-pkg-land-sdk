package filestation

import (
	"context"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
	"time"
)

func (*Client) CheckPermission(context.Context, *http.Request, string) error {
	return fail("unsupported")
}

// Keep the original DSMClient contract usable by existing read-only adapters.
// An adapter lacking the new method refuses the write check.
type permissionClient interface {
	CheckPermission(context.Context, *http.Request, string) error
}

func (s *Service) CheckPermission(ctx context.Context, caller api.Caller, r *http.Request, path string) error {
	if !validPath(path) {
		return fail("invalid-request")
	}
	auth, err := api.DSMSession(r)
	if err != nil {
		return err
	}
	r = r.Clone(ctx)
	r.Header.Set("X-SYNO-TOKEN", auth["dsm_synotoken"])
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.verify(ctx, caller, r); err != nil {
		return err
	}
	client, ok := s.client.(permissionClient)
	if !ok {
		return fail("unsupported")
	}
	return client.CheckPermission(ctx, r, path)
}
