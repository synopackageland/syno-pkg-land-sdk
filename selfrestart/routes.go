package selfrestart

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// Routes are always administrator-only inside the handler, even if an App
// declares a weaker route permission. Declare these methods in the App's
// operation registry; confirm/record are hidden protected-host operations.
func (s *Service) Routes() []api.Route {
	type params struct {
		Reference   string      `json:"reference"`
		Permit      string      `json:"permit,omitempty"`
		Observation Observation `json:"observation,omitempty"`
	}
	decode := func(raw json.RawMessage, out any) error {
		if len(raw) > 48<<10 {
			return api.BadRequest("invalid self restart request")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(out) != nil {
			return api.BadRequest("invalid self restart request")
		}
		if d.Decode(new(any)) != io.EOF {
			return api.BadRequest("invalid self restart request")
		}
		return nil
	}
	wrap := func(f func(context.Context, api.Caller, json.RawMessage) (any, error)) api.Handler {
		return func(ctx context.Context, c api.Caller, raw json.RawMessage) (any, error) {
			if err := s.authorize(c); err != nil {
				return nil, routeError(err)
			}
			out, err := f(ctx, c, raw)
			return out, routeError(err)
		}
	}
	return []api.Route{
		api.Handle("package.restart.reconciliation_preview", wrap(func(ctx context.Context, c api.Caller, raw json.RawMessage) (any, error) {
			var p struct {
				Reference string `json:"reference"`
			}
			if err := decode(raw, &p); err != nil {
				return nil, err
			}
			return s.ReconciliationPreview(ctx, c, p.Reference)
		})),
		api.Handle("package.restart.acknowledge", wrap(func(ctx context.Context, c api.Caller, raw json.RawMessage) (any, error) {
			var p struct {
				Reference string `json:"reference"`
				Approval  string `json:"approval"`
			}
			if err := decode(raw, &p); err != nil {
				return nil, err
			}
			return s.AcknowledgeUnknown(ctx, c, p.Reference, p.Approval)
		})),
		api.Handle("package.restart.draft", wrap(func(ctx context.Context, c api.Caller, raw json.RawMessage) (any, error) {
			var p struct{}
			if err := decode(raw, &p); err != nil {
				return nil, err
			}
			return s.Draft(ctx, c)
		})),
		api.Handle("package.restart.preview", wrap(func(ctx context.Context, c api.Caller, raw json.RawMessage) (any, error) {
			var p struct {
				Reference string `json:"reference"`
			}
			if err := decode(raw, &p); err != nil {
				return nil, err
			}
			return s.Preview(ctx, c, p.Reference)
		})),
		api.Handle("package.restart.get", wrap(func(ctx context.Context, c api.Caller, raw json.RawMessage) (any, error) {
			var p struct {
				Reference string `json:"reference"`
			}
			if err := decode(raw, &p); err != nil {
				return nil, err
			}
			return s.Get(ctx, c, p.Reference)
		})),
		api.Handle("package.restart.confirm", wrap(func(ctx context.Context, c api.Caller, raw json.RawMessage) (any, error) {
			var p struct {
				Reference string `json:"reference"`
				Approval  string `json:"approval"`
			}
			if err := decode(raw, &p); err != nil {
				return nil, err
			}
			return s.Confirm(ctx, c, p.Reference, p.Approval)
		})),
		api.Handle("package.restart.record", wrap(func(ctx context.Context, c api.Caller, raw json.RawMessage) (any, error) {
			var p params
			if err := decode(raw, &p); err != nil {
				return nil, err
			}
			return s.Record(ctx, c, p.Reference, p.Permit, p.Observation)
		})),
		api.Handle("package.restart.progress", wrap(func(ctx context.Context, c api.Caller, raw json.RawMessage) (any, error) {
			var p struct {
				Reference string `json:"reference"`
				Permit    string `json:"permit"`
				Phase     string `json:"phase"`
			}
			if err := decode(raw, &p); err != nil {
				return nil, err
			}
			return s.Progress(ctx, c, p.Reference, p.Permit, p.Phase)
		})),
	}
}

func routeError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return err
	}
	code := "self_restart_unavailable"
	switch {
	case errors.Is(err, ErrRefused):
		code = "self_restart_refused"
	case errors.Is(err, ErrBusy):
		code = "self_restart_busy"
	case errors.Is(err, ErrUnknown):
		code = "self_restart_unknown"
	}
	return &api.Error{Code: code, Message: "self restart requires reconciliation"}
}
