//go:build unix

package bridge

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/portal"
)

// The browser and App read the same entry. Without a reachable approved
// executor its registration is unknown, even if a retained alias exists.
func (sv services) readPortal(ctx context.Context) portal.Status {
	s := sv.portal()
	if sv.portalExecutor == nil {
		return s
	}
	entry, _ := portal.ExecutorEntry(ctx, sv.portalExecutor)
	return portal.WithEntry(s, entry)
}

func (sv services) portalStatus(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	if err := noParams(params); err != nil {
		return nil, err
	}
	return sv.readPortal(ctx), nil
}

// Only the fixed operations of the approved policy are available. The request
// supplies the administrator's session and original client address; the
// executor revalidates both and never accepts a caller-selected package.
func (sv services) portalChange(change func(context.Context, portal.Executor, map[string]string) (string, error)) api.RequestHandler {
	return func(ctx context.Context, caller api.Caller, req *http.Request, params json.RawMessage) (any, error) {
		if err := api.RequireAdmin(caller); err != nil {
			return nil, err
		}
		if err := noParams(params); err != nil {
			return nil, err
		}
		auth, err := api.DSMSession(req)
		if err != nil {
			return nil, err
		}
		if _, err = change(ctx, sv.portalExecutor, auth); err != nil {
			return nil, err
		}
		return sv.readPortal(ctx), nil
	}
}
