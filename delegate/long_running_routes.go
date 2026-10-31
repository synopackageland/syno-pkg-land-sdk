package delegate

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
	"strings"
)

// The immutable SDK role map is assembled by W1. No route accepts a caller
// chosen owner/UID, nor can an HTTP request use the sessionless daemon facade.
func (s *Service) longRunningRoutes() []api.Route {
	routes := []api.Route{}
	for _, method := range []string{"delegate.example.export", "delegate.destination", "delegate.select_destination", "delegate.move_from_staging", "delegate.operations.list", "delegate.operations.get", "delegate.operations.cancel", "delegate.disable", "delegate.enable", "delegate.audit"} {
		name := method
		routes = append(routes, api.HandleRequest(name, func(ctx context.Context, caller api.Caller, r *http.Request, raw json.RawMessage) (any, error) {
			if !caller.Authenticated() {
				return nil, fail("session_required")
			}
			auth, err := api.DSMSession(r)
			if err != nil {
				return nil, err
			}
			switch name {
			case "delegate.example.export":
				var in struct {
					Key   string `json:"key"`
					JobID string `json:"job_id"`
				}
				if err = strict(raw, &in); err != nil {
					return nil, err
				}
				return s.ExportExample(ctx, caller, r, in.Key, in.JobID)
			case "delegate.destination":
				var in struct {
					Key string `json:"key"`
				}
				if err = strict(raw, &in); err != nil {
					return nil, err
				}
				return s.Destination(ctx, caller, r, in.Key)
			case "delegate.operations.list":
				if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					return nil, api.BadRequest("empty parameters required")
				}
				if err = strict(raw, &struct{}{}); err != nil {
					return nil, err
				}
				return s.listOperations(ctx, caller, auth)
			case "delegate.select_destination":
				var in SelectDestinationParams
				if err = strict(raw, &in); err != nil {
					return nil, err
				}
				return s.SelectDestination(ctx, caller, r, in)
			case "delegate.move_from_staging":
				var in MoveParams
				if err = strict(raw, &in); err != nil {
					return nil, err
				}
				if err = validateMove(in); err != nil {
					return nil, err
				}
				if !strings.HasPrefix(in.Staging, OwnerStagingPrefix(caller.User(), in.DelegationID)) || !strings.HasSuffix(in.JobID, OwnerJobSuffix(caller.User(), in.DelegationID)) {
					return nil, fail(CodeInvalidRequest)
				}
				if err = s.verifyOperationOwner(ctx, caller, auth, in.DelegationID); err != nil {
					return nil, err
				}
				return s.admitStaging(ctx, auth, in)
			case "delegate.operations.get", "delegate.operations.cancel":
				var in OperationRef
				if err = strict(raw, &in); err != nil {
					return nil, err
				}
				if name == "delegate.operations.cancel" {
					return s.CancelOperation(ctx, auth, in)
				}
				return s.OperationStatus(ctx, auth, in)
			default:
				if (name == "delegate.disable" || name == "delegate.enable") && !caller.Admin() {
					return nil, fail("admin_required")
				}
				if err = strict(raw, &struct{}{}); err != nil {
					return nil, err
				}
				var out json.RawMessage
				err = s.Client.Call(ctx, name, auth, nil, &out)
				return out, err
			}
		}))
	}
	return routes
}
