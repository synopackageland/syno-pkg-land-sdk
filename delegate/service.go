package delegate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"io"
	"net/http"
	"net/url"
	"os/user"
	"strconv"
	"time"
)

func (s *Subject) UnmarshalJSON(b []byte) error {
	var v struct {
		Username string          `json:"username"`
		UID      json.RawMessage `json:"uid"`
	}
	if e := json.Unmarshal(b, &v); e != nil {
		return e
	}
	var n json.Number
	if json.Unmarshal(v.UID, &n) != nil {
		return errors.New("invalid subject UID")
	}
	s.Username = v.Username
	s.UID = n
	return nil
}

type ExecutorStatus struct {
	ExecutorDigest string          `json:"executor_digest"`
	PolicyDigest   string          `json:"policy_digest"`
	Version        string          `json:"version"`
	Ready          bool            `json:"ready"`
	State          string          `json:"state"`
	Disabled       *bool           `json:"disabled"`
	Selection      *ScopeSelection `json:"selection"`
	ScopeState     string          `json:"scope_state"`
	// Allowed answers a status sent with the caller's session: whether DSM's
	// Application Privileges let the caller use the App from its address.
	Allowed *bool `json:"allowed"`
}
type Service struct {
	ExecutorDigest, PolicyDigest string
	Client                       ExecutorClient
	Store                        *background.Store
	Scope, StagingDir            string
	LookupUID                    func(string) string
	HTTP                         *http.Client
	SessionFiles                 DestinationSession
	Destinations, Operations     []string
	Lifetime                     string
	InstallDraftPath             string
	// Work runs the App's own sha256-v1 jobs, with pause, progress and
	// checkpoints as under background.Runner; nil hashes the input.
	Work background.Work
	now  func() time.Time
	wait func(context.Context, time.Duration) error
}

func LookupUID(name string) string {
	u, e := user.Lookup(name)
	if e != nil {
		return ""
	}
	return u.Uid
}
func (s *Service) owner(c api.Caller) (background.Submitter, error) {
	lookup := s.LookupUID
	if lookup == nil {
		lookup = LookupUID
	}
	o := background.Submitter{User: c.User(), UID: lookup(c.User())}
	if !c.Authenticated() || !o.Valid() {
		return o, fail("identity_unavailable")
	}
	return o, nil
}
func strict(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return api.BadRequest("invalid parameters")
	}
	return nil
}

// lifecycle: the package is stopped or starting, so its executor does not
// answer yet; it waits for no approval, and the browser repeats the code.
func lifecycle(code string) bool {
	return code == CodeAppStopped || code == CodeAppStarting || code == CodeExecutorStarting
}

// Status reports the executor's state. With the caller's session (auth) it is
// blocked with reason APP_DENIED when DSM would refuse that caller the App,
// and with the lifecycle code while the package is stopped or starting.
func (s *Service) Status(ctx context.Context, auth map[string]string) (result Capability) {
	defer func() { s.decorateScope(&result) }()
	var out ExecutorStatus
	e := s.Client.Call(ctx, "status", auth, nil, &out)
	if e != nil {
		var a *api.Error
		if errors.As(e, &a) && (a.Code == CodeExecutorUnavailable || a.Code == CodeExecutorTimeout) {
			return Capability{State: "needs-admin-approval", Reason: a.Code}
		}
		if errors.As(e, &a) && lifecycle(a.Code) {
			return Capability{State: "blocked", Reason: a.Code}
		}
		return Capability{State: "blocked", Reason: "executor-status-refused"}
	}
	if !out.Ready && out.State == "starting" {
		return Capability{State: "blocked", Reason: CodeExecutorStarting}
	}
	if (s.ExecutorDigest != "" && out.ExecutorDigest != s.ExecutorDigest) || (s.PolicyDigest != "" && out.PolicyDigest != s.PolicyDigest) || !out.Ready || out.Disabled == nil || out.ExecutorDigest == "" || out.PolicyDigest == "" || out.Version == "" {
		return Capability{State: "blocked", Reason: "executor-not-ready"}
	}
	if auth != nil && (out.Allowed == nil || !*out.Allowed) {
		return Capability{State: "blocked", Reason: CodeAppDenied}
	}
	selection := &ScopeSelection{Scope: s.Scope, Root: virtualScope(s.Scope)}
	if s.Scope == "install-selected" {
		selection = out.Selection
		if selection == nil && out.ScopeState == "needs-scope-confirmation" {
			return Capability{State: "needs-scope-confirmation", Disabled: out.Disabled}
		}
		if out.ScopeState != "" || !validSelection(selection, true) {
			return Capability{State: "blocked", Reason: "executor-scope-invalid"}
		}
	}
	if *out.Disabled {
		return Capability{State: "disabled", Disabled: out.Disabled, Selection: selection}
	}
	return Capability{State: "ready", Disabled: out.Disabled, Selection: selection}
}

type CreateParams struct {
	JobID string   `json:"job_id"`
	Ops   []string `json:"ops"`
	Path  string   `json:"path"`
	TTL   int      `json:"ttl_seconds"`
}

func (s *Service) create(ctx context.Context, r *http.Request, in CreateParams) (Grant, error) {
	var out Grant
	auth, e := api.DSMSession(r)
	if e != nil {
		return out, e
	}
	if in.JobID == "" || !validPath(in.Path) || in.TTL <= 0 || in.TTL > 3600 || len(in.Ops) == 0 || len(in.Ops) > 10 {
		return out, fail(CodeInvalidRequest)
	}
	for _, op := range in.Ops {
		switch op {
		case "files.list", "files.info", "files.createFolder", "files.checkPermission", "files.write", "files.read", "files.rename", "files.copy", "files.move", "files.delete":
		default:
			return out, fail(CodeInvalidRequest)
		}
	}
	e = s.Client.Call(ctx, "delegate.create", auth, map[string]any{"job_id": in.JobID, "ops": in.Ops, "scope": s.Scope, "path": in.Path, "ttl_seconds": in.TTL}, &out)
	if e == nil && (out.DelegationID == "" || out.Subject.Username == "" || out.Subject.UIDString() == "" || out.ExpiresAt.IsZero()) {
		e = fail(CodeExecutorProtocol)
	}
	return out, e
}
func (s *Service) revokeJob(ctx context.Context, b background.DelegationBinding) error {
	return s.Client.Call(ctx, "delegate.revoke", nil, map[string]string{"delegation_id": b.DelegationID, "job_id": b.JobID}, nil)
}
func (s *Service) Sweep(ctx context.Context) error {
	bindings, e := s.Store.DelegationCleanup(ctx)
	if e != nil {
		return e
	}
	var result error
	for _, b := range bindings {
		if e = s.revokeJob(ctx, b); e == nil {
			e = s.Store.ForgetDelegation(ctx, b.JobID)
		}
		result = errors.Join(result, e)
	}
	return errors.Join(result, s.SweepMoves(ctx))
}
func (s *Service) Routes() []api.Route {
	routes := []api.Route{}
	for _, method := range []string{"delegate.status", "delegate.create", "delegate.list", "delegate.revoke", "delegate.jobs.submit", "delegate.jobs.list", "delegate.jobs.cancel"} {
		m := method
		routes = append(routes, api.HandleRequest(m, func(ctx context.Context, c api.Caller, r *http.Request, p json.RawMessage) (any, error) {
			return s.handle(ctx, c, r, m, p)
		}))
	}
	return append(routes, s.longRunningRoutes()...)
}
func (s *Service) handle(ctx context.Context, c api.Caller, r *http.Request, method string, p json.RawMessage) (any, error) {
	if !c.Authenticated() {
		return nil, fail("session_required")
	}
	switch method {
	case "delegate.status":
		if e := strict(p, &struct{}{}); e != nil {
			return nil, e
		}
		auth, e := api.DSMSession(r)
		if e != nil {
			return nil, e
		}
		out := s.Status(ctx, auth)
		if lifecycle(out.Reason) {
			return nil, fail(out.Reason)
		}
		out.Lifetime = s.Lifetime
		out.Destinations = append([]string(nil), s.Destinations...)
		return out, nil
	case "delegate.create":
		var in CreateParams
		if e := strict(p, &in); e != nil {
			return nil, e
		}
		return s.create(ctx, r, in)
	case "delegate.list":
		if e := strict(p, &struct{}{}); e != nil {
			return nil, e
		}
		auth, e := api.DSMSession(r)
		if e != nil {
			return nil, e
		}
		var out json.RawMessage
		e = s.Client.Call(ctx, "delegate.list", auth, nil, &out)
		return out, e
	case "delegate.revoke":
		var in struct {
			ID string `json:"delegation_id"`
		}
		if e := strict(p, &in); e != nil || in.ID == "" {
			return nil, api.BadRequest("delegation_id required")
		}
		auth, e := api.DSMSession(r)
		if e != nil {
			return nil, e
		}
		// Stop local work first; failed socket revocation remains durable cleanup.
		owner, err := s.owner(c)
		if err != nil {
			return nil, err
		}
		jobs, err := s.Store.ListFor(ctx, background.Access{Owner: owner})
		if err != nil {
			return nil, jobError(err)
		}
		for _, job := range jobs {
			if job.DelegationID == in.ID {
				if _, err = s.Store.CancelFor(ctx, job.ID, background.Access{Owner: owner}); err != nil && !errors.Is(err, background.ErrNotCancelable) {
					return nil, jobError(err)
				}
			}
		}
		e = s.Client.Call(ctx, "delegate.revoke", auth, map[string]string{"delegation_id": in.ID}, nil)
		return map[string]bool{"revoked": e == nil}, e
	}
	owner, e := s.owner(c)
	if e != nil {
		return nil, e
	}
	access := background.Access{Owner: owner}
	switch method {
	case "delegate.jobs.submit":
		var in struct {
			Key string `json:"key"`
			DownloadInput
		}
		if e := strict(p, &in); e != nil {
			return nil, e
		}
		if !validDownload(in.DownloadInput) {
			return nil, fail(CodeInvalidRequest)
		}
		data, _ := json.Marshal(in.DownloadInput)
		job, e := s.Store.SubmitDelegated(ctx, background.SubmitRequest{Key: in.Key, Name: in.Name, Data: data, Submitter: owner}, func(id string) (background.DelegationBinding, error) {
			grant, e := s.create(ctx, r, CreateParams{JobID: id, Ops: []string{"files.write"}, Path: in.Dest, TTL: 3600})
			if e != nil {
				return background.DelegationBinding{}, e
			}
			if grant.Subject.Username != owner.User || grant.Subject.UIDString() != owner.UID {
				cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
				defer done()
				s.revokeJob(cleanup, background.DelegationBinding{JobID: id, DelegationID: grant.DelegationID})
				return background.DelegationBinding{}, fail("identity_unavailable")
			}
			return background.DelegationBinding{DelegationID: grant.DelegationID, ExpiresAt: grant.ExpiresAt}, nil
		}, func(b background.DelegationBinding) {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			if s.revokeJob(cleanup, b) == nil {
				_ = s.Store.ForgetDelegation(cleanup, b.JobID)
			}
		})
		return job, jobError(e)
	case "delegate.jobs.list":
		if e := strict(p, &struct{}{}); e != nil {
			return nil, e
		}
		jobs, e := s.Store.ListDelegatedFor(ctx, access)
		return map[string]any{"jobs": jobs}, jobError(e)
	case "delegate.jobs.cancel":
		var in struct {
			ID string `json:"id"`
		}
		if e := strict(p, &in); e != nil {
			return nil, e
		}
		job, e := s.Store.GetFor(ctx, in.ID, access)
		if e != nil || job.Worker != background.WorkerDownloadV1 {
			return nil, fail("not_found")
		}
		job, e = s.Store.CancelFor(ctx, in.ID, access)
		if e != nil {
			return nil, jobError(e)
		}
		e = s.Sweep(ctx)
		if e != nil {
			return map[string]any{"job": job, "revocation_pending": true}, nil
		}
		return map[string]any{"job": job, "revocation_pending": false}, nil
	}
	return nil, fail("unsupported")
}

// JobError maps a background.Store failure to the code the delegate routes
// answer with, for other front ends of the same store.
func JobError(e error) error { return jobError(e) }

// jobError maps a store failure to a stable code. jobs_busy, canceled and
// jobs_unavailable wrote nothing a retry could duplicate; spool_unsafe and
// journal_corrupt stay terminal until the spool is repaired.
func jobError(e error) error {
	if e == nil {
		return nil
	}
	var a *api.Error
	if errors.As(e, &a) {
		return e
	}
	codes := map[error]string{context.Canceled: "canceled", context.DeadlineExceeded: "canceled", background.ErrUnsafeSpool: "spool_unsafe", background.ErrJournalUnreadable: "journal_corrupt", background.ErrJournalSchema: "journal_corrupt", background.ErrBusy: "jobs_busy", background.ErrNotFound: "not_found", background.ErrNotCancelable: "not_cancelable", background.ErrKeyConflict: "key_conflict", background.ErrOwnerQueueFull: "queue_full", background.ErrQueueFull: "queue_full", background.ErrOwnerQuota: "quota", background.ErrQuota: "quota", background.ErrInvalid: CodeInvalidRequest}
	for err, code := range codes {
		if errors.Is(e, err) {
			return fail(code)
		}
	}
	return fail("jobs_unavailable")
}

type DownloadInput struct {
	URL  string `json:"url"`
	Dest string `json:"dest"`
	Name string `json:"name"`
}

func validDownload(in DownloadInput) bool {
	u, e := url.Parse(in.URL)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Fragment == "" && len(in.URL) <= 4096 && validPath(in.Dest) && validName(in.Name)
}

// ParseUID is useful when assembling a trusted job owner from executor output.
func (s Subject) UIDString() string {
	n, e := strconv.ParseUint(s.UID.String(), 10, 32)
	if e != nil || n == 0 {
		return ""
	}
	return strconv.FormatUint(n, 10)
}
