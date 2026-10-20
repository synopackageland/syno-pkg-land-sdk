//go:build unix

package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"github.com/synopackageland/syno-pkg-land-sdk/delegate"
	"github.com/synopackageland/syno-pkg-land-sdk/filestation"
	"github.com/synopackageland/syno-pkg-land-sdk/portal"
)

// RootTimeout is the longest a root.* service waits for the executor, for
// root.run; the other root ops wait delegate.ExecutorTimeout
// (spec/protocol/v1, Timeouts). The App's request deadline still ends it first.
const RootTimeout = delegate.RootRunTimeout

// rootOps are the executor's root operations the App may ask for. The
// executor's policy and DSM decide who may; the runtime adds the session.
var rootOps = []string{"root.run", "root.start", "root.jobs.list", "root.jobs.get", "root.jobs.stop"}

// services are what the runtime offers the App, by capability. Each acts for
// the call's verified caller, with the request's DSM session where it needs
// one; the App never names a user and never sees the session.
type services struct {
	webAPI         bool
	webAPIOnly     bool
	dataDir        string
	maxSpool       int64
	portal         func() portal.Status
	portalExecutor portal.Executor
	files          *filestation.Service
	delegate       *delegate.Service
	jobs           *background.Store
	executor       delegate.ExecutorClient
	// executorRoutes are executor.status and executor.control, for the browser.
	executorRoutes []api.Route
	// lookupUID resolves the caller's UID for job ownership.
	lookupUID func(string) string
}

func (sv services) table() map[string]Service {
	table := map[string]Service{}
	if sv.files != nil {
		routes := routeMap(sv.files.Routes())
		table["files.list"] = signedIn(routes["nas.files.list"])
		table["files.info"] = signedIn(routes["nas.files.info"])
		table["files.read"] = signedIn(sv.read)
	}
	if sv.portal != nil {
		table["portal.status"] = func(ctx context.Context, _ *Call, params json.RawMessage) (any, error) {
			if err := noParams(params); err != nil {
				return nil, err
			}
			return sv.readPortal(ctx), nil
		}
	}
	if sv.delegate != nil {
		routes := routeMap(sv.delegate.Routes())
		table["delegate.status"] = signedIn(routes["delegate.status"])
		table["delegate.move_from_staging"] = signedIn(routes["delegate.move_from_staging"])
		table["delegate.operation"] = signedIn(routes["delegate.operations.get"])
		table["delegate.discard_staging"] = signedIn(sv.discardStaging)
	}
	if sv.jobs != nil {
		table["bg.submit"] = signedIn(sv.submit)
		table["bg.get"] = signedIn(sv.job(sv.jobs.GetFor))
		table["bg.cancel"] = signedIn(sv.job(sv.jobs.CancelFor))
	}
	if sv.executor != nil {
		if sv.webAPI {
			table["webapi.grant"] = signedIn(sv.webAPIGrant)
			for _, pair := range [][2]string{{"webapi.call", "webapi.call"}, {"webapi.grants", "delegate.list"}, {"webapi.revoke", "delegate.revoke"}} {
				table[pair[0]] = signedIn(sv.root(pair[1]))
			}
		}
		if !sv.webAPIOnly {
			for _, op := range rootOps {
				table[op] = signedIn(sv.root(op))
			}
		}
	}
	return table
}

func (sv services) webAPIGrant(ctx context.Context, call *Call, params json.RawMessage) (any, error) {
	var in struct {
		Scope    string `json:"scope"`
		Job      string `json:"job_id,omitempty"`
		TTL      int64  `json:"ttl_seconds,omitempty"`
		Lifetime string `json:"lifetime,omitempty"`
	}
	if err := strict(params, &in); err != nil {
		return nil, err
	}
	p := map[string]any{"scope": in.Scope, "ops": []string{"webapi.call"}}
	if in.Lifetime == "until-revoked" {
		if in.Job != "" || in.TTL != 0 {
			return nil, api.BadRequest("invalid WebAPI grant lifetime")
		}
		p["lifetime"] = in.Lifetime
	} else {
		if in.Lifetime != "" {
			return nil, api.BadRequest("invalid WebAPI grant lifetime")
		}
		p["job_id"], p["ttl_seconds"] = in.Job, in.TTL
	}
	raw, _ := json.Marshal(p)
	return sv.root("delegate.create")(ctx, call, raw)
}

// routeMap offers SDK routes as services, so a service runs the very code
// the browser's route runs.
func routeMap(routes []api.Route) map[string]Service {
	m := map[string]Service{}
	for _, r := range routes {
		m[r.Method()] = func(ctx context.Context, call *Call, params json.RawMessage) (any, error) {
			return r.Invoke(ctx, call.Caller, call.Request, params)
		}
	}
	return m
}

// signedIn offers a service to a signed-in caller only: a public method's
// call has no one to act for.
func signedIn(s Service) Service {
	return func(ctx context.Context, call *Call, params json.RawMessage) (any, error) {
		if !call.Caller.Authenticated() {
			return nil, &api.Error{Code: "session_required", Message: "this service acts for a signed-in user"}
		}
		return s(ctx, call, params)
	}
}

func strict(raw json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(out) != nil || dec.More() {
		return api.BadRequest("invalid svc params")
	}
	return nil
}

// The codes the services add (spec/bridge/v1/errors.json, kind service): an
// App may pass them on, and the browser's SDK translates them.
func limitExceeded() *api.Error {
	return &api.Error{Code: "limit_exceeded", Message: "The file is larger than allowed."}
}

func readFailed() *api.Error {
	return &api.Error{Code: "read_failed", Message: "Unable to read the file. Please try again later."}
}

func identityUnavailable() *api.Error {
	return &api.Error{Code: "identity_unavailable", Message: "Unable to identify this user account. Please try again later."}
}

// read copies a NAS file the caller may read, through File Station with the
// caller's session, into the call's spool; the App reads it there until the
// call ends.
func (sv services) read(ctx context.Context, call *Call, params json.RawMessage) (any, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := strict(params, &in); err != nil {
		return nil, err
	}
	download, err := sv.files.Download(ctx, call.Caller, call.Request, in.Path)
	if err != nil {
		return nil, err
	}
	defer download.Body.Close()
	if download.ContentLength > sv.maxSpool {
		return nil, limitExceeded()
	}
	f, err := call.spool.create("read-" + strconv.Itoa(len(call.spool.files)))
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(f, io.LimitReader(contextReader{ctx, download.Body}, sv.maxSpool+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case n > sv.maxSpool:
		return nil, limitExceeded()
	case err != nil || (download.ContentLength >= 0 && n != download.ContentLength):
		return nil, readFailed()
	}
	return map[string]any{"path": f.Name(), "size": n}, nil
}

func (sv services) discardStaging(ctx context.Context, _ *Call, params json.RawMessage) (any, error) {
	var ref delegate.OperationRef
	if err := strict(params, &ref); err != nil {
		return nil, err
	}
	if err := sv.delegate.DiscardStaging(ctx, ref); err != nil {
		return nil, err
	}
	return map[string]bool{"discarded": true}, nil
}

// owner is the job owner: the verified caller, never one the App names.
func (sv services) owner(call *Call) (background.Access, error) {
	owner := background.Submitter{User: call.Caller.User(), UID: sv.lookupUID(call.Caller.User())}
	if !owner.Valid() {
		return background.Access{}, identityUnavailable()
	}
	return background.Access{Owner: owner}, nil
}

// submit queues a job whose input is a file in the data directory.
func (sv services) submit(ctx context.Context, call *Call, params json.RawMessage) (any, error) {
	var in struct {
		Key   string `json:"key"`
		Name  string `json:"name"`
		Input string `json:"input"`
	}
	if err := strict(params, &in); err != nil {
		return nil, err
	}
	access, err := sv.owner(call)
	if err != nil {
		return nil, err
	}
	data, err := sv.input(in.Input)
	if err != nil {
		return nil, err
	}
	job, duplicate, err := sv.jobs.Submit(ctx, background.SubmitRequest{Key: in.Key, Name: in.Name, Data: data, Submitter: access.Owner})
	if err != nil {
		return nil, delegate.JobError(err)
	}
	return map[string]any{"job": job, "duplicate": duplicate}, nil
}

// input reads a job's input from inside the data directory, never through a
// link out of it, up to background.MaxInputBytes.
func (sv services) input(name string) ([]byte, error) {
	bad := api.BadRequest("input must be a file in the data directory")
	rel, err := filepath.Rel(sv.dataDir, name)
	if err != nil || !filepath.IsAbs(name) || !filepath.IsLocal(rel) {
		return nil, bad
	}
	root, err := os.OpenRoot(sv.dataDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return nil, bad
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, background.MaxInputBytes+1))
	if err != nil {
		return nil, bad
	}
	if len(data) > background.MaxInputBytes {
		return nil, limitExceeded()
	}
	return data, nil
}

// job is bg.get or bg.cancel: one of the caller's own jobs.
func (sv services) job(do func(context.Context, string, background.Access) (background.Job, error)) Service {
	return func(ctx context.Context, call *Call, params json.RawMessage) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := strict(params, &in); err != nil {
			return nil, err
		}
		access, err := sv.owner(call)
		if err != nil {
			return nil, err
		}
		job, err := do(ctx, in.ID, access)
		if err != nil {
			return nil, delegate.JobError(err)
		}
		return job, nil
	}
}

// root forwards a root operation to the executor with the request's DSM
// session as auth. The App gives only the operation's params; the executor
// verifies the session with DSM and checks its policy and the App privilege.
// Its answer reaches the App as it came.
func (sv services) root(op string) Service {
	return func(ctx context.Context, call *Call, params json.RawMessage) (any, error) {
		in := map[string]json.RawMessage{}
		if len(params) > 0 && string(params) != "null" {
			if err := strict(params, &in); err != nil {
				return nil, err
			}
		}
		auth, err := api.DSMSession(call.Request)
		if err != nil {
			return nil, err
		}
		var out json.RawMessage
		if err := sv.executor.Call(ctx, op, auth, in, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}
