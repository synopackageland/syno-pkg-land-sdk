package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/budget"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

const version = "1"
const maxRequest = 64 << 10

var allOps = []string{"webapi.call", "status", "delegate.create", "delegate.list", "delegate.revoke", "files.list", "files.info", "files.createFolder", "files.checkPermission", "files.write", "files.read", "files.rename", "files.copy", "files.move", "files.delete", "files.taskStatus", "files.taskCancel", "files.moveFromStaging", "files.operationStatus", "files.cancelOperation", "delegate.disable", "delegate.enable", "delegate.audit", "root.run", "root.start", "root.jobs.list", "root.jobs.get", "root.jobs.stop", "portal.register", "portal.unregister"}
var tokenRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
var destinationRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var idRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Policy struct {
	Timeouts     *budget.Policy       `json:"timeouts,omitempty"`
	Version      int                  `json:"v"`
	Package      string               `json:"package"`
	App          string               `json:"app"`
	Entry        string               `json:"entry,omitempty"`
	Scope        string               `json:"scope,omitempty"`
	Operations   []string             `json:"operations"`
	Destinations []string             `json:"destinations,omitempty"`
	Lifetime     string               `json:"lifetime,omitempty"`
	Root         map[string]RootEntry `json:"root,omitempty"`
	// Purge names the root op that runs at uninstall while the approval is live.
	Purge string `json:"purge,omitempty"`
	// Stop names the root op that runs when the package stops or upgrades.
	Stop string `json:"stop,omitempty"`
	// Start names the root op that runs at the first start after an install
	// or upgrade.
	Start string `json:"start,omitempty"`
	// RootState gives root programs a directory only root can read.
	RootState bool                     `json:"root_state,omitempty"`
	Portal    *PortalPolicy            `json:"portal,omitempty"`
	WebAPI    map[string]WebAPIProfile `json:"webapi,omitempty"`
}

// WebAPIProfile is an administrator-approved set of exact user API calls.
// Its JSON bytes participate in the ordinary policy digest and approval anchor.
type WebAPIProfile struct {
	Purpose string                      `json:"purpose"`
	APIs    map[string]map[string][]int `json:"apis"`
	// Zero preserves JSON-only profiles and their existing digest.
	RawMaxBytes int64 `json:"raw_max_bytes,omitempty"`
	// Zero keeps the profile without request uploads and its existing digest.
	UploadMaxBytes int64 `json:"upload_max_bytes,omitempty"`
}

// PortalPolicy is the Application Portal entry the executor registers in
// DSM's app.d: DSM 7.1 refuses the web-config resource of unsigned packages.
type PortalPolicy struct {
	Application string `json:"application"`
	Alias       string `json:"alias"`
}
type Subject struct {
	Username string `json:"username"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	Groups   []int  `json:"groups"`
	Home     string `json:"home"`
	Expired  string `json:"expired"`
	Admin    bool   `json:"admin"`
}
type Delegation struct {
	WebAPIProfileDigest string            `json:"webapi_profile_digest,omitempty"`
	ScopeGeneration     uint64            `json:"scope_generation,omitempty"`
	Lifetime            string            `json:"lifetime,omitempty"`
	DestinationKey      string            `json:"destination,omitempty"`
	Directory           directoryIdentity `json:"directory,omitempty"`
	ID                  string            `json:"delegation_id"`
	Package             string            `json:"package"`
	Subject             Subject           `json:"subject"`
	Job                 string            `json:"job_id"`
	Ops                 []string          `json:"ops"`
	Scope               string            `json:"scope"`
	Path                string            `json:"path"`
	Root                string            `json:"root"`
	Expires             time.Time         `json:"expires_at"`
	Revoked             bool              `json:"revoked"`
	// ClientAddr is the browser address that created the grant; work the
	// grant does without a session is checked from it.
	ClientAddr string `json:"client_addr,omitempty"`
}
type Auth struct {
	Cookie    string `json:"dsm_cookie,omitempty"`
	SynoToken string `json:"dsm_synotoken,omitempty"`
	// RemoteAddr is the browser's address (REMOTE_ADDR), against which DSM
	// evaluates the caller's App Privileges.
	RemoteAddr string `json:"remote_addr,omitempty"`
	hasKeys    bool
}

// present reports caller authentication: any auth key on the wire, even an
// empty one, or a credential.
func (a Auth) present() bool { return a.hasKeys || a.Cookie != "" || a.SynoToken != "" }

// Preserve wire key presence: an explicit empty credential is caller auth.
func (a *Auth) UnmarshalJSON(b []byte) error {
	type plainAuth Auth
	var decoded plainAuth
	if err := strictJSON(b, &decoded); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	for key := range keys {
		if key != "dsm_cookie" && key != "dsm_synotoken" && key != "remote_addr" {
			return errors.New("auth key")
		}
	}
	*a = Auth(decoded)
	a.hasKeys = len(keys) > 0 || bytes.Equal(bytes.TrimSpace(b), []byte("null"))
	return nil
}

type Request struct {
	DeadlineMS int64           `json:"deadline_ms,omitempty"`
	Version    int             `json:"v"`
	Op         string          `json:"op"`
	Auth       Auth            `json:"auth"`
	Params     json.RawMessage `json:"params"`
}
type Response struct {
	OK      bool   `json:"ok"`
	Result  any    `json:"result,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
type Params struct {
	Lifetime       string   `json:"lifetime,omitempty"`
	DestinationKey string   `json:"destination,omitempty"`
	Size           int64    `json:"size,omitempty"`
	Job            string   `json:"job_id,omitempty"`
	Ops            []string `json:"ops,omitempty"`
	Scope          string   `json:"scope,omitempty"`
	Path           string   `json:"path,omitempty"`
	TTL            int64    `json:"ttl_seconds,omitempty"`
	ID             string   `json:"delegation_id,omitempty"`
	Staging        string   `json:"staging,omitempty"`
	Dest           string   `json:"dest_dir,omitempty"`
	Name           string   `json:"name,omitempty"`
	Conflict       string   `json:"conflict,omitempty"`
	Parents        bool     `json:"parents,omitempty"`
	Recursive      bool     `json:"recursive,omitempty"`
	Permanent      bool     `json:"permanent,omitempty"`
	TaskID         string   `json:"task_id,omitempty"`
	MaxBytes       int64    `json:"max_bytes,omitempty"`
	// RootParams is the params object root.run and root.start hand the program.
	RootParams json.RawMessage `json:"params,omitempty"`
}
type DSM interface {
	Verify(context.Context, string, string) (string, error)
	Account(context.Context, string) (Subject, error)
	Allow(context.Context, Subject, string, string) error
	ScopeRoot(context.Context, Subject, string) (string, error)
	API(context.Context, string, string, map[string]any) (json.RawMessage, error)
}
type writeJobKey struct{}

type Writer interface {
	Write(context.Context, Subject, string, string, string, string) (MoveReceipt, error)
}
type Executor struct {
	Policy      Policy
	DSM         DSM
	Writer      Writer
	Tasks       FileTasks
	Reader      FileReader
	Runner      RootRunner
	RootStaging RootStaging
	// Uploads consumes App-staged webapi.call upload bytes.
	Uploads UploadStore
	// StopStep runs the policy's stop step for a package stop or upgrade.
	StopStep func(reason string)
	// PackageVersion is the INFO version of the package being served.
	PackageVersion string
	// RootState is the root state directory, when the policy has one.
	RootState string
	Portal    PortalEntry
	// TaskCheck is the executor's own readback of its approval tasks.
	TaskCheck                    *taskCheck
	Store                        *Store
	Now                          func() time.Time
	ExecutorDigest, PolicyDigest string
	legacyCalls                  map[string][]chan struct{}
	copySlot                     chan struct{}
	commitMu                     sync.Mutex // Durable admission/revoke/disable and bounded publish only.
	async                        *operationRuntime
	taskMu                       sync.Mutex
	portalMu                     sync.Mutex // One portal register or unregister at a time.
	shutdownUntil                time.Time
	rootMu                       sync.Mutex
	rootRunning                  int
	jobs                         *rootJobs
	jobEndReason                 string
	starting                     atomic.Bool // A due start step has not ended.
	tasks                        map[string]*fileTask
}

func deny(code string) Response {
	return Response{Code: code, Message: strings.ToLower(strings.ReplaceAll(code, "_", " "))}
}
func success(v any) Response { return Response{OK: true, Result: v} }
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func validName(s string) bool {
	return s != "" && !strings.Contains(s, "/") && !strings.Contains(s, "..") && !strings.ContainsFunc(s, unicode.IsControl) && utf8.ValidString(s)
}
func cleanPath(p string) bool {
	return strings.HasPrefix(p, "/") && filepath.Clean(p) == p && !strings.ContainsFunc(p, unicode.IsControl) && utf8.ValidString(p)
}
func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/")
}
func strictJSON(b []byte, v any) error {
	if _, ok := v.(*webAPIRequest); ok {
		return strictWebJSON(b, v, "delegation_id job_id api method version params response_type upload")
	}
	if _, ok := v.(*Request); ok {
		var head map[string]json.RawMessage
		if json.Unmarshal(b, &head) == nil && string(head["op"]) == `"webapi.call"` {
			return strictWebJSON(b, v, "v op auth params deadline_ms")
		}
	}

	if !utf8.Valid(b) {
		return errors.New("utf8")
	}
	// encoding/json otherwise silently accepts duplicate keys, including auth.
	d := json.NewDecoder(bytes.NewReader(b))
	_, params := v.(*Params)
	_, auth := v.(*Auth)
	_, request := v.(*Request)
	if err := uniqueValue(d, params || auth, request); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

// Match encoding/json's Unicode case folding when detecting duplicate keys.
func foldJSONKey(s string) string {
	return strings.Map(func(r rune) rune {
		folded := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < folded {
				folded = next
			}
		}
		return folded
	}, s)
}

// canonical holds keys to [a-z0-9_]; request marks a request's own members,
// where auth and params open canonical objects. A policy's root program or
// schema property may be named auth or params.
func uniqueValue(d *json.Decoder, canonical, request bool) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	del, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch del {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := k.(string)
			folded := foldJSONKey(s)
			if !ok || seen[folded] {
				return errors.New("duplicate key")
			}
			seen[folded] = true
			credentialObject := request && (strings.EqualFold(s, "auth") || strings.EqualFold(s, "params"))
			if credentialObject && s != "auth" && s != "params" {
				return errors.New("noncanonical key")
			}
			if canonical && strings.ContainsFunc(s, func(r rune) bool { return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') }) {
				return errors.New("noncanonical key")
			}
			if err = uniqueValue(d, canonical || credentialObject, false); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err = uniqueValue(d, canonical, false); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON")
	}
	_, err = d.Token()
	return err
}
func (e *Executor) Connection(parent context.Context, c net.Conn, uid int, peer func(net.Conn) (int, error)) {
	got, err := peer(c)
	if err != nil || (got != uid && got != 0) {
		return
	}
	c.SetDeadline(time.Now().Add(15 * time.Second))
	// Only root.run and root.start may exceed 64 KiB: they carry params.
	reader := bufio.NewReader(c)
	line, err := readBatchLine(reader, maxRootRequest)
	var out Response
	var req Request
	if len(line) > maxRootRequest {
		out = deny("REQUEST_TOO_LARGE")
	} else if err != nil {
		out = deny("INVALID_REQUEST")
	} else if strictJSON(line, &req) != nil {
		out = deny("INVALID_REQUEST")
		if len(line) > maxRequest {
			out = deny("REQUEST_TOO_LARGE")
		}
	} else if len(line) > maxRequest && req.Op != "root.run" && req.Op != "root.start" {
		out = deny("REQUEST_TOO_LARGE")
	} else if req.Op == "root.batch" {
		e.batchConnection(parent, c, reader, req, got, uid)
		return
	} else {
		bound := executorOpBound
		if req.Op == "root.run" {
			bound = rootRunRequestBy
		} else if req.DeadlineMS > 0 {
			bound = e.Policy.budgets().Duration("operation")
		}
		if req.DeadlineMS < 0 || req.DeadlineMS > 3600000 {
			json.NewEncoder(c).Encode(deny("INVALID_REQUEST"))
			return
		}
		if req.DeadlineMS > 0 {
			bound = min(bound, time.Duration(req.DeadlineMS)*time.Millisecond)
		}
		ctx, cancel := context.WithTimeout(parent, bound)
		defer cancel()
		out = e.handlePeer(ctx, req, got, uid)
	}
	c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	json.NewEncoder(c).Encode(out)
}

// grantedLocked finds the person's active grant for the same destination key
// and the same folder. Selecting it again, as a client retrying a create it
// timed out on does, keeps that grant and its saves instead of replacing it.
func (e *Executor) grantedLocked(d Delegation) (Delegation, bool, error) {
	ds, err := e.Store.List()
	if err != nil {
		return Delegation{}, false, err
	}
	for _, prior := range ds {
		if prior.Package == d.Package && prior.Subject.UID == d.Subject.UID && prior.Subject.Username == d.Subject.Username && prior.Lifetime == d.Lifetime && prior.DestinationKey == d.DestinationKey && !prior.Revoked && prior.Path == d.Path && prior.Root == d.Root && prior.Directory == d.Directory && prior.Scope == d.Scope && prior.ScopeGeneration == d.ScopeGeneration {
			return prior, true, nil
		}
	}
	return Delegation{}, false, nil
}
func (e *Executor) eligible(ctx context.Context, name string, uid int, files bool) (Subject, string) {
	moveValidationStage(ctx, "account")
	s, err := e.DSM.Account(ctx, name)
	if errors.Is(err, verificationUnavailable) {
		return Subject{}, "INTERNAL"
	}
	if moveValidationDeadline(ctx, err) {
		return s, "EXECUTOR_PAUSED"
	}
	if err != nil {
		return Subject{}, "INELIGIBLE"
	}
	return e.privileged(ctx, s, name, uid, files)
}

// privileged checks a looked-up account can be delegated and may use the App
// and, for files, File Station.
func (e *Executor) privileged(ctx context.Context, s Subject, name string, uid int, files bool) (Subject, string) {
	if s.Username != name || s.Expired != "normal" || s.UID < 1024 || s.GID < 0 || len(s.Groups) == 0 || (uid >= 0 && s.UID != uid) {
		return Subject{}, "INELIGIBLE"
	}
	moveValidationStage(ctx, "app-privilege")
	if err := e.DSM.Allow(ctx, s, e.Policy.App, e.Policy.Entry); err != nil {
		if errors.Is(err, verificationUnavailable) {
			return Subject{}, "INTERNAL"
		}
		if moveValidationDeadline(ctx, err) {
			return s, "EXECUTOR_PAUSED"
		}
		return Subject{}, "APP_DENIED"
	}
	moveValidationStage(ctx, "file-station-privilege")
	if files {
		if err := e.DSM.Allow(ctx, s, "SYNO.SDS.App.FileStation3.Instance", ""); err != nil {
			if errors.Is(err, verificationUnavailable) {
				return Subject{}, "INTERNAL"
			}
			if moveValidationDeadline(ctx, err) {
				return s, "EXECUTOR_PAUSED"
			}
			return Subject{}, "APP_DENIED"
		}
	}
	return s, ""
}
func (e *Executor) verified(ctx context.Context, auth Auth) (Subject, string) {
	if batch, ok := ctx.Value(batchVerifiedKey{}).(batchVerified); ok {
		if batch.auth.Cookie != auth.Cookie || batch.auth.SynoToken != auth.SynoToken || batch.auth.RemoteAddr != auth.RemoteAddr {
			return Subject{}, "AUTH_DENIED"
		}
		return batch.subject, ""
	}
	if auth.Cookie == "" || len(auth.Cookie) > 4096 || (strings.Contains(auth.Cookie, ";") || strings.ContainsFunc(auth.Cookie, func(r rune) bool { return r < 0x20 || r == 0x7f })) || auth.SynoToken == "" || len(auth.SynoToken) > 4096 || strings.ContainsFunc(auth.SynoToken, unicode.IsControl) {
		return Subject{}, "AUTH_DENIED"
	}
	name, err := e.DSM.Verify(ctx, auth.Cookie, auth.SynoToken)
	if err != nil || name == "" {
		return Subject{}, "AUTH_DENIED"
	}
	s, err := e.DSM.Account(ctx, name)
	if errors.Is(err, verificationUnavailable) {
		return Subject{}, "INTERNAL"
	}
	if err != nil || s.Username != name || s.Expired != "normal" {
		return Subject{}, "AUTH_DENIED"
	}
	return s, ""
}
func (e *Executor) Handle(ctx context.Context, r Request) Response {
	ctx = withRequestAddress(ctx, r.Auth)
	if r.Op != "status" && e.starting.Load() {
		return deny("EXECUTOR_STARTING")
	}
	if isExtension(r.Op) {
		return e.handleExtension(ctx, r)
	}
	if ctx.Err() != nil {
		return deny("EXECUTOR_PAUSED")
	}
	if r.Version != 1 {
		return deny("INVALID_REQUEST")
	}
	if !contains(allOps, r.Op) {
		return deny("UNKNOWN_OP")
	}
	if !contains(e.Policy.Operations, r.Op) && r.Op != "files.checkPermission" {
		return deny("POLICY_DENIED")
	}
	if r.Op == "webapi.call" {
		return e.callWebAPI(ctx, r)
	}
	var p Params
	if len(r.Params) == 0 {
		r.Params = json.RawMessage(`{}`)
	}
	if strictJSON(r.Params, &p) != nil {
		return deny("INVALID_REQUEST")
	}
	// Reject parameters belonging to another operation, rather than silently ignoring them.
	var keys map[string]json.RawMessage
	if json.Unmarshal(r.Params, &keys) != nil {
		return deny("INVALID_REQUEST")
	}
	allowed := map[string]string{"status": "", "delegate.create": "job_id ops scope path ttl_seconds destination lifetime", "delegate.list": "", "delegate.revoke": "delegation_id job_id", "files.list": "delegation_id job_id path", "files.info": "delegation_id job_id path", "files.createFolder": "delegation_id job_id path name parents", "files.checkPermission": "delegation_id job_id path", "files.rename": "delegation_id job_id path name", "files.read": "delegation_id job_id path max_bytes", "files.copy": "delegation_id job_id path dest_dir conflict", "files.move": "delegation_id job_id path dest_dir conflict", "files.delete": "delegation_id job_id path recursive permanent", "files.taskStatus": "delegation_id job_id task_id", "files.taskCancel": "delegation_id job_id task_id", "files.write": "delegation_id job_id staging dest_dir name conflict", "root.run": "name params", "root.start": "name params", "root.jobs.list": "", "root.jobs.get": "job_id", "root.jobs.stop": "job_id", "portal.register": "", "portal.unregister": ""}
	for k := range keys {
		if !contains(strings.Fields(allowed[r.Op]), k) {
			return deny("INVALID_REQUEST")
		}
	}
	if r.Op == "delegate.create" || strings.HasPrefix(r.Op, "files.") {
		e.commitMu.Lock()
		disabled, err := e.Store.Disabled()
		e.commitMu.Unlock()
		if err != nil {
			return deny("STORAGE_ERROR")
		}
		if disabled {
			return deny("disabled")
		}
	}
	if r.Op == "files.checkPermission" {
		if d, err := e.Store.Get(p.ID); err == nil && d.Lifetime == "until-revoked" {
			_, hasJob := keys["job_id"]
			return e.checkDestination(ctx, r, p, d, hasJob)
		}
		if !contains(e.Policy.Operations, r.Op) {
			return deny("POLICY_DENIED")
		}
	}
	switch r.Op {
	case "status":
		// With the caller's session, status also answers whether DSM lets the
		// caller use the App and File Station from its address.
		judged, allowed := r.Auth.present(), false
		if judged {
			s, code := e.verified(ctx, r.Auth)
			if code == "" {
				_, code = e.privileged(ctx, s, s.Username, s.UID, true)
			}
			if code != "" && code != "APP_DENIED" && code != "INELIGIBLE" {
				return deny(code)
			}
			allowed = code == ""
		}
		e.commitMu.Lock()
		defer e.commitMu.Unlock()
		disabled, err := e.Store.Disabled()
		out := map[string]any{"executor_digest": e.ExecutorDigest, "policy_digest": e.PolicyDigest, "version": version, "ready": err == nil, "disabled": disabled}
		if judged {
			out["allowed"] = allowed
		}
		if e.starting.Load() {
			out["ready"], out["state"] = false, "starting"
		}
		if e.Policy.Portal != nil {
			out["portal"] = PortalCleanupRequired
			if e.Portal != nil {
				out["portal"] = e.Portal.Entry()
			}
		}
		if e.TaskCheck != nil {
			out["tasks"] = e.TaskCheck.Status()
		}
		if e.Policy.Start != "" {
			out["start_step"] = startStepStatus(e.Store, e.PackageVersion)
		}
		if e.Policy.Scope == installSelectedScope {
			state, code := e.selectionLocked()
			out["scope_state"] = code
			out["selection"] = nil
			if code == "" {
				out["selection"] = map[string]any{"scope": state.Scope, "root": scopeVirtual(state.Scope), "generation": state.Generation}
			}
			if code == "STORAGE_ERROR" {
				out["ready"] = false
			}
		}
		return success(out)
	case "delegate.create":
		if contains(p.Ops, "webapi.call") {
			return e.createWebAPI(ctx, r.Auth, p, keys)
		}
		long := p.Lifetime == "until-revoked"
		if long {
			if _, ok := keys["job_id"]; ok {
				return deny("INVALID_REQUEST")
			}
			if _, ok := keys["ttl_seconds"]; ok {
				return deny("INVALID_REQUEST")
			}
			if !contains(e.Policy.Destinations, p.DestinationKey) {
				return deny("invalid-destination")
			}
		}
		if (long && (e.Policy.Lifetime != p.Lifetime || !contains(e.Policy.Destinations, p.DestinationKey) || p.Job != "" || p.TTL != 0 || len(p.Ops) != 1 || p.Ops[0] != "files.moveFromStaging")) || (!long && (p.Lifetime != "" || p.DestinationKey != "" || !tokenRE.MatchString(p.Job) || p.TTL <= 0 || p.TTL > 86400*30 || contains(p.Ops, "files.moveFromStaging"))) || len(p.Ops) == 0 || p.Scope != e.Policy.Scope || !cleanPath(p.Path) {
			return deny("INVALID_REQUEST")
		}
		for _, op := range p.Ops {
			// Task status and cancel follow the copy, move or delete that started a task.
			if contains(taskOps, op) {
				return deny("INVALID_REQUEST")
			}
		}
		for _, op := range p.Ops {
			if !strings.HasPrefix(op, "files.") || !contains(allOps, op) || !contains(e.Policy.Operations, op) {
				return deny("POLICY_DENIED")
			}
		}
		s, code := e.verified(ctx, r.Auth)
		if code != "" {
			return deny(code)
		}
		s, code = e.privileged(ctx, s, s.Username, s.UID, true)
		if code != "" {
			return deny(code)
		}
		selection, selectionCode := e.selection()
		if selectionCode != "" {
			return deny(selectionCode)
		}
		root, err := e.DSM.ScopeRoot(ctx, s, selection.Scope)
		if err != nil {
			return deny("SCOPE_DENIED")
		}
		d := Delegation{Package: e.Policy.Package, Subject: s, Job: p.Job, Ops: p.Ops, Scope: selection.Scope, ScopeGeneration: selection.Generation, Path: scopeVirtual(selection.Scope), Root: root, Expires: e.Now().UTC().Add(time.Duration(p.TTL) * time.Second)}
		d.ClientAddr, _ = clientAddress(ctx)
		var real string
		if long {
			real, err = mappedDirectory(d, p.Path)
		} else {
			real, err = scopedPath(d, p.Path)
		}
		if err != nil {
			return deny("SCOPE_DENIED")
		}
		d.Root = real
		d.Path = p.Path
		if long {
			d.Lifetime = p.Lifetime
			d.DestinationKey = p.DestinationKey
			d.Expires = time.Time{}
			d.Directory, err = bindDirectory(real)
			if err != nil {
				return deny("SCOPE_DENIED")
			}
		}
		// Permission creation is checked as the subject, never SYSTEM_ADMIN.
		if _, err = e.DSM.API(ctx, s.Username, "files.checkPermission", map[string]any{"path": p.Path}); err != nil {
			return deny("PERMISSION_DENIED")
		}
		id := make([]byte, 32)
		if _, err = rand.Read(id); err != nil {
			return deny("INTERNAL")
		}
		d.ID = hex.EncodeToString(id)
		e.commitMu.Lock()
		disabled, disableErr := e.Store.Disabled()
		if !e.scopeMatchesLocked(d) {
			e.commitMu.Unlock()
			return deny("SCOPE_DENIED")
		}
		if disableErr != nil || disabled {
			e.commitMu.Unlock()
			return deny("disabled")
		}
		if long {
			if prior, found, listErr := e.grantedLocked(d); listErr != nil || found {
				// The browser choosing the folder again starts its later saves.
				if found && prior.ClientAddr != d.ClientAddr {
					prior.ClientAddr = d.ClientAddr
					listErr = e.Store.Put(prior)
				}
				e.commitMu.Unlock()
				if listErr != nil {
					return deny("STORAGE_ERROR")
				}
				return success(publicDelegation(prior))
			}
		}
		putErr := e.auditLocked(s, "delegate.create", "", d.Path, "admitting")
		// A destination key denotes one current choice for this person. Changing
		// it cancels its previous grant under the same publish fence.
		if putErr == nil && long {
			var previous []Delegation
			previous, putErr = e.Store.List()
			for _, prior := range previous {
				if putErr != nil {
					break
				}
				if prior.Package != d.Package || prior.Subject.UID != s.UID || prior.Subject.Username != s.Username || prior.Lifetime != "until-revoked" || prior.DestinationKey != d.DestinationKey || prior.Revoked {
					continue
				}
				putErr = e.auditLocked(s, "delegate.revoke", prior.ID, prior.Path, "destination-replaced")
				if putErr == nil {
					prior.Revoked = true
					putErr = e.Store.Put(prior)
				}
				if putErr == nil {
					e.cancelGrantLocked(prior.ID, "destination-replaced")
				}
			}
		}
		if putErr == nil {
			putErr = e.Store.Put(d)
		}
		e.commitMu.Unlock()
		if putErr != nil {
			return deny("STORAGE_ERROR")
		}
		if long {
			return success(publicDelegation(d))
		}
		return success(map[string]any{"delegation_id": d.ID, "subject": publicSubject(s), "expires_at": d.Expires})
	case "delegate.list":
		s, code := e.verified(ctx, r.Auth)
		if code != "" {
			return deny(code)
		}
		ds, err := e.Store.List()
		if err != nil {
			return deny("STORAGE_ERROR")
		}
		out := []any{}
		for _, d := range ds {
			if d.Package == e.Policy.Package && d.Subject.UID == s.UID && d.Subject.Username == s.Username {
				if !e.scopeMatches(d) {
					d.Revoked = true
				}
				out = append(out, publicDelegation(d))
			}
		}
		return success(map[string]any{"delegations": out})
	case "delegate.revoke":
		d, err := e.Store.Get(p.ID)
		if err != nil || d.Package != e.Policy.Package {
			return deny("DELEGATION_DENIED")
		}
		if r.Auth.present() {
			s, code := e.verified(ctx, r.Auth)
			if code != "" {
				return deny(code)
			}
			if (d.Lifetime != "" || !s.Admin) && (s.UID != d.Subject.UID || s.Username != d.Subject.Username) {
				return deny("DELEGATION_DENIED")
			}
		} else if d.Lifetime != "" || p.Job == "" || p.Job != d.Job {
			return deny("DELEGATION_DENIED")
		}
		e.commitMu.Lock()
		locked := true
		defer func() {
			if locked {
				e.commitMu.Unlock()
			}
		}()
		d, err = e.Store.Get(p.ID)
		if err != nil {
			return deny("STORAGE_ERROR")
		}
		d.Revoked = true
		if e.Store.Put(d) != nil {
			return deny("STORAGE_ERROR")
		}
		e.cancelGrantLocked(d.ID, "grant-revoked")
		if e.auditLocked(d.Subject, "delegate.revoke", "", d.Path, "revoked") != nil {
			return deny("STORAGE_ERROR")
		}
		pending := e.legacyPendingLocked(d.ID)
		e.commitMu.Unlock()
		locked = false
		if waitLegacy(ctx, pending) != nil {
			return deny("EXECUTOR_PAUSED")
		}
		e.stopTasks(d.ID)
		return success(map[string]any{"revoked": true})
	case "root.run":
		return e.rootRun(ctx, r.Auth, p)
	case "root.start":
		return e.rootStart(ctx, r.Auth, p)
	case "root.jobs.list", "root.jobs.get", "root.jobs.stop":
		return e.rootJobOp(ctx, r.Op, r.Auth, p)
	case "portal.register", "portal.unregister":
		s, code := e.verified(ctx, r.Auth)
		if code != "" {
			return deny(code)
		}
		if !s.Admin {
			return deny("ADMIN_REQUIRED")
		}
		if err := e.DSM.Allow(ctx, s, e.Policy.App, e.Policy.Entry); err != nil {
			return deny(dsmRefusal(err, "APP_DENIED"))
		}
		if e.Portal == nil {
			return deny("PORTAL_FAILED")
		}
		change := e.Portal.Register
		if r.Op == "portal.unregister" {
			change = e.Portal.Unregister
		}
		for !e.portalMu.TryLock() {
			select {
			case <-ctx.Done():
				return deny("PORTAL_FAILED")
			case <-time.After(50 * time.Millisecond):
			}
		}
		defer e.portalMu.Unlock()
		if ctx.Err() != nil {
			return deny("PORTAL_FAILED")
		}
		if change(ctx) != nil {
			return deny("PORTAL_FAILED")
		}
		return success(map[string]any{"entry": e.Portal.Entry()})
	}
	d, err := e.Store.Get(p.ID)
	if err != nil || d.Package != e.Policy.Package || d.Revoked || (d.Lifetime != "" || !e.Now().Before(d.Expires)) || d.Job != p.Job || !(contains(d.Ops, r.Op) || contains(taskOps, r.Op)) || !e.scopeMatches(d) {
		return deny("DELEGATION_DENIED")
	}
	ctx = withGrantAddress(ctx, r.Auth, d.ClientAddr)
	s, code := e.eligible(ctx, d.Subject.Username, d.Subject.UID, true)
	if code != "" {
		return deny(code)
	}
	root, err := e.DSM.ScopeRoot(ctx, s, d.Scope)
	if err != nil {
		return deny("SCOPE_DENIED")
	}
	// Revalidate the root so a moved home/share never inherits an old delegation.
	check := d
	check.Root = root
	check.Path = scopeVirtual(d.Scope)
	real, err := scopedPath(check, d.Path)
	if err != nil || real != d.Root {
		return deny("SCOPE_DENIED")
	}
	if contains(taskOps, r.Op) {
		return e.followTask(ctx, r.Op, d, p)
	}
	path := p.Path
	if r.Op == "files.write" {
		path = p.Dest
	}
	if !cleanPath(path) {
		return deny("INVALID_REQUEST")
	}
	if !within(d.Path, path) {
		return deny("SCOPE_DENIED")
	}
	if r.Op == "files.createFolder" && fileOnPath(d, path) {
		return deny("destination-not-directory")
	}
	if r.Op == "files.createFolder" && p.Parents {
		real, err = scopedTarget(d, path)
	} else {
		real, err = scopedPath(d, path)
	}
	if err != nil {
		return deny(scopeCode(err))
	}
	if r.Op == "files.read" {
		_, has := keys["max_bytes"]
		return e.readFile(ctx, d, s, real, p, has)
	}
	if taskKinds[r.Op] != "" {
		_, has := keys["conflict"]
		return e.startTask(ctx, r.Op, d, s, p, has)
	}
	if r.Op == "files.write" {
		if _, has := keys["conflict"]; !validName(p.Name) || !validRelative(p.Staging) || (has && !validConflict(p.Conflict)) {
			return deny("INVALID_REQUEST")
		}
		release, err := e.acquireCopy(ctx)
		if err != nil {
			return deny("EXECUTOR_PAUSED")
		}
		defer release()
		e.commitMu.Lock()
		disabled, x := e.Store.Disabled()
		fresh, x2 := e.Store.Get(d.ID)
		e.commitMu.Unlock()
		if x != nil || x2 != nil {
			return deny("STORAGE_ERROR")
		}
		if disabled {
			return deny("disabled")
		}
		if fresh.Revoked || ctx.Err() != nil {
			return deny("DELEGATION_DENIED")
		}
		writerCtx := context.WithValue(ctx, writeJobKey{}, p.Job)
		writerCtx = context.WithValue(writerCtx, writeConflictKey{}, storedConflict(p.Conflict))
		writerCtx = context.WithValue(writerCtx, legacyPublishKey{}, func(publish func() (MoveReceipt, error)) (MoveReceipt, error) {
			current, c := e.eligible(ctx, d.Subject.Username, d.Subject.UID, true)
			if c != "" || !sameIdentity(s, current) {
				return MoveReceipt{}, errors.New("identity changed")
			}
			if _, x := e.DSM.API(ctx, s.Username, "files.checkPermission", map[string]any{"path": p.Dest}); x != nil {
				return MoveReceipt{}, x
			}
			e.commitMu.Lock()
			defer e.commitMu.Unlock()
			disabled, x := e.Store.Disabled()
			fresh, x2 := e.Store.Get(d.ID)
			if x != nil || x2 != nil || disabled || fresh.Revoked || !e.Now().Before(fresh.Expires) || ctx.Err() != nil {
				return MoveReceipt{}, errors.New("write fenced")
			}
			return publish()
		})
		out, err := e.Writer.Write(writerCtx, s, p.Staging, real, p.Name, d.Root)
		if err != nil {
			return deny("WRITE_FAILED")
		}
		name := receiptName(Operation{Name: p.Name, Conflict: storedConflict(p.Conflict)}, out)
		return success(map[string]any{"path": strings.TrimSuffix(p.Dest, "/") + "/" + name})
	}
	args := map[string]any{"path": path}
	if r.Op == "files.rename" && path == d.Path {
		// Renaming the granted folder itself would move the grant.
		return deny("SCOPE_DENIED")
	}
	if r.Op == "files.createFolder" || r.Op == "files.rename" {
		if !validName(p.Name) {
			return deny("INVALID_REQUEST")
		}
		args["name"] = p.Name
		if p.Parents {
			args["parents"] = true
		}
	}
	done, code := e.beginLegacy(d)
	if code != "" {
		return deny(code)
	}
	defer done()
	out, err := e.DSM.API(ctx, s.Username, r.Op, args)
	if err != nil {
		return deny(fileStationCode(err))
	}
	if r.Op == "files.createFolder" && !reportedWithin(d, out, "folders", path+"/"+p.Name) {
		return deny("SCOPE_DENIED")
	}
	if r.Op == "files.rename" && !reportedWithin(d, out, "files", filepath.Dir(path)+"/"+p.Name) {
		return deny("SCOPE_DENIED")
	}
	return success(out)
}
func publicSubject(s Subject) any { return map[string]any{"username": s.Username, "uid": s.UID} }
func publicDelegation(d Delegation) any {
	out := map[string]any{"delegation_id": d.ID, "subject": publicSubject(d.Subject), "job_id": d.Job, "ops": d.Ops, "scope": d.Scope, "path": d.Path, "expires_at": d.Expires, "revoked": d.Revoked, "destination": d.DestinationKey, "lifetime": d.Lifetime}
	if d.ScopeGeneration != 0 {
		out["scope_generation"] = d.ScopeGeneration
	}
	return out
}
func scopeVirtual(scope string) string {
	if scope == "home" {
		return "/home"
	}
	if strings.HasPrefix(scope, "shared:") && validName(strings.TrimPrefix(scope, "shared:")) {
		return "/" + strings.TrimPrefix(scope, "shared:")
	}
	return ""
}
func scopedPath(d Delegation, p string) (string, error) {
	virtual := scopeVirtual(d.Scope)
	if virtual == "" || !cleanPath(p) || !within(virtual, p) {
		return "", errors.New("scope")
	}
	// Root is the bound delegation directory; Path is its File Station spelling.
	base := d.Path
	if base == "" {
		base = virtual
	}
	if !within(base, p) {
		return "", errors.New("scope")
	}
	joined := filepath.Join(d.Root, strings.TrimPrefix(strings.TrimPrefix(p, base), "/"))
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", missingWithin(d.Root, joined)
	}
	if !within(d.Root, real) {
		return "", errors.New("scope")
	}
	return real, nil
}

// errNotFound is a path inside the grant that does not exist.
var errNotFound = errors.New("not found")

// missingWithin tells a missing path from one that escapes: the nearest
// existing folder above it must still resolve inside root.
func missingWithin(root, joined string) error {
	for parent := filepath.Dir(joined); within(root, parent); parent = filepath.Dir(parent) {
		real, err := filepath.EvalSymlinks(parent)
		if err != nil {
			continue
		}
		if within(root, real) {
			return errNotFound
		}
		break
	}
	return errors.New("scope")
}

// scopeCode is the refusal for a path scopedPath did not resolve.
func scopeCode(err error) string {
	if errors.Is(err, errNotFound) {
		return "not-found"
	}
	return "SCOPE_DENIED"
}

// fileOnPath reports whether the nearest existing entry at or above p is a
// file, so a folder cannot be created there.
func fileOnPath(d Delegation, p string) bool {
	for q := p; within(d.Path, q); q = filepath.Dir(q) {
		real, err := scopedPath(d, q)
		if err == nil {
			info, err := os.Stat(real)
			return err == nil && !info.IsDir()
		}
		if !errors.Is(err, errNotFound) || q == d.Path {
			return false
		}
	}
	return false
}

// scopedTarget is scopedPath for a folder that may not exist yet: missing
// components are allowed only below an existing ancestor inside the root.
func scopedTarget(d Delegation, p string) (string, error) {
	real, err := scopedPath(d, p)
	if err == nil {
		return real, nil
	}
	base := d.Path
	if base == "" {
		base = scopeVirtual(d.Scope)
	}
	if !cleanPath(p) || p == base || !within(base, p) {
		return "", err
	}
	missing := filepath.Join(d.Root, strings.TrimPrefix(strings.TrimPrefix(p, base), "/"))
	if _, x := os.Lstat(missing); !errors.Is(x, fs.ErrNotExist) {
		return "", err
	}
	parent, err := scopedTarget(d, filepath.Dir(p))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(p)), nil
}

// reportedWithin re-checks the single entry File Station reports under key:
// it must be exactly want and still resolve inside the delegation.
func reportedWithin(d Delegation, data json.RawMessage, key, want string) bool {
	var out map[string][]struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(data, &out) != nil || len(out[key]) != 1 || out[key][0].Path != want {
		return false
	}
	_, err := scopedPath(d, want)
	return err == nil
}
func validRelative(p string) bool {
	if p == "" || filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if !validName(part) || part == "." {
			return false
		}
	}
	return true
}

// Permanent destinations reject every symlink component; legacy File Station
// lookup retains its historical canonical-path semantics.
func mappedDirectory(d Delegation, p string) (string, error) {
	base := d.Path
	if base == "" {
		base = scopeVirtual(d.Scope)
	}
	if base == "" || !cleanPath(d.Root) || !cleanPath(p) || !within(base, p) {
		return "", errors.New("scope")
	}
	real := filepath.Join(d.Root, strings.TrimPrefix(strings.TrimPrefix(p, base), "/"))
	if _, err := bindDirectory(real); err != nil {
		return "", err
	}
	return real, nil
}

func (p Policy) budgets() budget.Policy {
	if p.Timeouts != nil {
		return *p.Timeouts
	}
	return budget.Default()
}
