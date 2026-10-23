package delegate

import (
	"context"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
)

// CheckDestination rechecks the current owner's saved destination with the
// already approved executor. It performs no browser-session HTTP relay and
// gives no authority outside that destination. Browser-only Apps do not use
// this API or acquire an executor; use createDsmFileStation in their frontend.
func (s *Service) CheckDestination(ctx context.Context, caller api.Caller, r *http.Request, key, path string) error {
	state, err := s.Destination(ctx, caller, r, key)
	if err != nil {
		return err
	}
	if state.Status != "active" || state.Destination == nil || state.Destination.Path != path {
		return fail(CodeInvalidDestination)
	}
	auth, err := api.DSMSession(r)
	if err != nil {
		return err
	}
	var out struct {
		Allowed bool `json:"allowed"`
	}
	err = s.Client.Call(ctx, "files.checkPermission", auth, map[string]string{"delegation_id": state.Destination.ID, "path": path}, &out)
	if err != nil {
		return err
	}
	if !out.Allowed {
		return fail(CodePermissionDenied)
	}
	return nil
}
