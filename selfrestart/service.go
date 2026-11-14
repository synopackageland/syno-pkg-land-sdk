// Package selfrestart owns admission and durable readback for a fixed package
// restart. DSM's native browser transport executes the stop/start batch; no
// package worker, root helper, scheduler or saved DSM credential is involved.
package selfrestart

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

const MaxWorkSample = 10

var (
	ErrRefused = errors.New("self restart refused")
	ErrUnknown = errors.New("self restart reference unavailable")
	ErrBusy    = errors.New("a self restart already needs reconciliation")
	identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
	reference  = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Policy is an installed, immutable declaration, hashed into the App build.
// New reads it from PolicyPath and verifies SHA256 on every admission/readback.
// Neither this path nor its digest may come from a request.
type Policy struct {
	Schema  int    `json:"schema"`
	Package string `json:"package"`
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
}

// Approval binds the existing protected confirmation to this exact preview.
// Consume must authenticate its one-use handle, require a fresh administrator,
// check actor/ref/digest and consume it atomically. AI calls cannot mint handles.
// A nil consumer refuses construction; client booleans are never approval.
type Approval interface {
	Consume(context.Context, api.Caller, string, string, string) error
}

type Work struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
}

type Preview struct {
	Reference     string   `json:"reference"`
	Package       string   `json:"package"`
	Version       string   `json:"version"`
	Digest        string   `json:"digest"`
	Action        string   `json:"action"`
	Interruptions []string `json:"interruptions"`
	Work          []Work   `json:"work"`
	WorkTruncated bool     `json:"work_truncated"`
}

type Operation struct {
	Reference string    `json:"reference"`
	Package   string    `json:"package"`
	Version   string    `json:"version"`
	State     string    `json:"state"`
	Reason    string    `json:"reason,omitempty"`
	LastPhase string    `json:"last_phase,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

// Dispatch is returned only by the first successful Confirm. A repeated
// Confirm returns the existing operation with Dispatch=false, never another
// permit. Keep Permit in the protected native host; do not expose it to an AI.
type Dispatch struct {
	Operation Operation `json:"operation"`
	Dispatch  bool      `json:"dispatch"`
	Permit    string    `json:"permit,omitempty"`
	Preview   Preview   `json:"preview"`
}

type Config struct {
	PolicyPath   string
	PolicySHA256 string
	StateDir     string
	Approval     Approval
	// ActiveWork is trusted App assembly. Return at most MaxWorkSample+1
	// sanitized references; never include prompts, arguments or credentials.
	ActiveWork func(context.Context) ([]Work, error)
}

type Service struct {
	config Config
	policy Policy
}

func New(config Config) (*Service, error) {
	if config.Approval == nil || config.ActiveWork == nil || !reference.MatchString(config.PolicySHA256) || config.StateDir == "" || os.Geteuid() == 0 {
		return nil, ErrRefused
	}
	s := &Service{config: config}
	p, err := s.readPolicy()
	if err != nil {
		return nil, err
	}
	s.policy = p
	return s, nil
}

func (s *Service) readPolicy() (Policy, error) {
	var p Policy
	f, err := os.Open(s.config.PolicyPath)
	if err != nil {
		return p, ErrRefused
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 {
		return p, ErrRefused
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != s.config.PolicySHA256 || json.Unmarshal(b, &p) != nil || p.Schema != 1 || !p.Enabled || !identifier.MatchString(p.Package) || len(p.Version) == 0 || len(p.Version) > 128 {
		return Policy{}, ErrRefused
	}
	return p, nil
}

func (s *Service) authorize(caller api.Caller) error {
	if s == nil {
		return ErrRefused
	}
	if err := api.RequireAdmin(caller); err != nil {
		return err
	}
	p, err := s.readPolicy()
	if err != nil || p != s.policy {
		return ErrRefused
	}
	return nil
}

func randomID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Service) Draft(ctx context.Context, caller api.Caller) (Operation, error) {
	if err := s.authorize(caller); err != nil {
		return Operation{}, err
	}
	id, err := randomID()
	if err != nil {
		return Operation{}, err
	}
	now := time.Now().UTC()
	op := Operation{Reference: id, Package: s.policy.Package, Version: s.policy.Version, State: "draft", Created: now, Updated: now}
	err = s.journal(ctx, func(j *journal) error {
		if len(j.Records) >= 100 {
			oldest := ""
			for key, r := range j.Records {
				if r.State != "confirmed-running" && r.State != "refused" && r.State != "failed" && !r.Acknowledged {
					continue
				}
				if oldest == "" || r.Created.Before(j.Records[oldest].Created) {
					oldest = key
				}
			}
			if oldest == "" {
				return ErrBusy
			}
			delete(j.Records, oldest)
		}
		j.Records[id] = record{Operation: op, Owner: caller.User()}
		return nil
	})
	return op, err
}

func (s *Service) preview(ctx context.Context, op Operation) (Preview, error) {
	work, err := s.config.ActiveWork(ctx)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Reference: op.Reference, Package: op.Package, Version: op.Version,
		Action:        "restart",
		Interruptions: []string{"all-apps", "ai-turns", "transfers", "workers"}, Work: []Work{}, WorkTruncated: len(work) > MaxWorkSample}
	for _, w := range work[:min(len(work), MaxWorkSample)] {
		if !identifier.MatchString(w.Kind) || len(w.Reference) == 0 || len(w.Reference) > 128 {
			return Preview{}, ErrRefused
		}
		p.Work = append(p.Work, w)
	}
	digestPreview(&p)
	return p, nil
}

func (s *Service) Preview(ctx context.Context, caller api.Caller, ref string) (Preview, error) {
	op, err := s.Get(ctx, caller, ref)
	if err != nil {
		return Preview{}, err
	}
	return s.preview(ctx, op)
}

func (s *Service) Get(ctx context.Context, caller api.Caller, ref string) (Operation, error) {
	if err := s.authorize(caller); err != nil {
		return Operation{}, err
	}
	var op Operation
	err := s.journal(ctx, func(j *journal) error {
		r, ok := j.Records[ref]
		if !reference.MatchString(ref) || !ok || r.Owner != caller.User() {
			return ErrUnknown
		}
		op = r.Operation
		if op.State == "accepted" || op.State == "stopping" || op.State == "starting" {
			op.LastPhase = op.State
			op.State = "unknown"
			op.Reason = "native-result-unavailable"
		}
		return nil
	})
	return op, err
}

// Confirm durably spends the sole dispatch permit before returning it. Losing
// this reply is deliberately ambiguous: read the original reference, never
// Confirm with a new reference or re-send the native mutation automatically.
func (s *Service) Confirm(ctx context.Context, caller api.Caller, ref, approval string) (Dispatch, error) {
	if err := s.authorize(caller); err != nil {
		return Dispatch{}, err
	}
	var out Dispatch
	refused := false
	err := s.journal(ctx, func(j *journal) error {
		r, ok := j.Records[ref]
		if !reference.MatchString(ref) || !ok || r.Owner != caller.User() {
			return ErrUnknown
		}
		out.Operation = r.Operation
		if r.State != "draft" {
			return nil
		}
		if r.Package != s.policy.Package || r.Version != s.policy.Version {
			return ErrRefused
		}
		for _, prior := range j.Records {
			if prior.Reference != ref && !prior.Acknowledged && (prior.State == "accepted" || prior.State == "stopping" || prior.State == "starting" || prior.State == "unknown") {
				return ErrBusy
			}
		}
		p, err := s.preview(ctx, r.Operation)
		if err != nil {
			return err
		}
		if approval == "" || len(approval) > 4096 {
			return ErrRefused
		}
		if err = s.config.Approval.Consume(ctx, caller, ref, p.Digest, approval); err != nil {
			r.State = "refused"
			r.Reason = "approval-refused"
			r.Updated = time.Now().UTC()
			j.Records[ref] = r
			out.Operation = r.Operation
			refused = true
			return nil
		}
		// Policy may have been revoked while confirmation was being consumed.
		if err = s.authorize(caller); err != nil {
			return err
		}
		permit, err := randomID()
		if err != nil {
			return err
		}
		h := sha256.Sum256([]byte(permit))
		r.PermitHash = hex.EncodeToString(h[:])
		r.State = "accepted"
		r.Updated = time.Now().UTC()
		j.Records[ref] = r
		out = Dispatch{Operation: r.Operation, Dispatch: true, Permit: permit, Preview: p}
		return nil
	})
	if err != nil {
		return Dispatch{}, err
	}
	if refused {
		return out, ErrRefused
	}
	return out, nil
}

// Observation is a native-host observation, not a credential or a backend
// attestation. The protected host must retain ownership of Permit and Record.
// Missing native results can only record unknown, even if DSM says running.
type Observation struct {
	Bulk    json.RawMessage `json:"bulk"`
	Current json.RawMessage `json:"current"`
}

func (s *Service) Record(ctx context.Context, caller api.Caller, ref, permit string, observation Observation) (Operation, error) {
	if err := s.authorize(caller); err != nil {
		return Operation{}, err
	}
	var op Operation
	err := s.journal(ctx, func(j *journal) error {
		r, ok := j.Records[ref]
		if !reference.MatchString(ref) || !ok || r.Owner != caller.User() || !reference.MatchString(permit) {
			return ErrUnknown
		}
		h := sha256.Sum256([]byte(permit))
		if r.PermitHash == "" || r.PermitHash != hex.EncodeToString(h[:]) {
			return ErrRefused
		}
		// Terminal records cannot be overwritten by a later observation.
		if r.Acknowledged || r.State == "confirmed-running" || r.State == "refused" || r.State == "failed" {
			op = r.Operation
			return nil
		}
		r.State, r.Reason = classify(s.policy, observation)
		r.Updated = time.Now().UTC()
		j.Records[ref] = r
		op = r.Operation
		return nil
	})
	return op, err
}

func digestPreview(p *Preview) {
	p.Digest = ""
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	p.Digest = hex.EncodeToString(h[:])
}

func reconciliationPreview(op Operation) Preview {
	p := Preview{Reference: op.Reference, Package: op.Package, Version: op.Version, Action: "acknowledge-unknown", Interruptions: []string{}, Work: []Work{}}
	digestPreview(&p)
	return p
}

// ReconciliationPreview is a separate confirmation: acknowledge manual
// investigation of an uncertain result. It authorizes no lifecycle action.
func (s *Service) ReconciliationPreview(ctx context.Context, c api.Caller, ref string) (Preview, error) {
	op, err := s.Get(ctx, c, ref)
	if err != nil {
		return Preview{}, err
	}
	if op.State != "unknown" {
		return Preview{}, ErrRefused
	}
	return reconciliationPreview(op), nil
}

// AcknowledgeUnknown releases the singleton after an administrator manually
// reconciles Package Center. The original outcome stays unknown, its permit is
// never reissued and this method never starts/stops anything. A deliberate
// future restart needs a new draft and protected confirmation.
func (s *Service) AcknowledgeUnknown(ctx context.Context, c api.Caller, ref, approval string) (Operation, error) {
	if err := s.authorize(c); err != nil {
		return Operation{}, err
	}
	var op Operation
	err := s.journal(ctx, func(j *journal) error {
		r, ok := j.Records[ref]
		if !ok || r.Owner != c.User() {
			return ErrUnknown
		}
		if r.Acknowledged {
			op = r.Operation
			return nil
		}
		if r.State != "accepted" && r.State != "stopping" && r.State != "starting" && r.State != "unknown" {
			return ErrRefused
		}
		p := reconciliationPreview(r.Operation)
		if approval == "" || len(approval) > 4096 {
			return ErrRefused
		}
		if err := s.config.Approval.Consume(ctx, c, ref, p.Digest, approval); err != nil {
			return err
		}
		if err := s.authorize(c); err != nil {
			return err
		}
		r.Acknowledged = true
		r.State = "unknown"
		r.Reason = "operator-acknowledged"
		r.Updated = time.Now().UTC()
		j.Records[ref] = r
		op = r.Operation
		return nil
	})
	return op, err
}

// Progress accepts only monotonic diagnostic phases from the same native host.
// An unknown phase never grants another dispatch permit. Only Record can
// confirm success from the full native stop/start result and running readback.
func (s *Service) Progress(ctx context.Context, caller api.Caller, ref, permit, phase string) (Operation, error) {
	if err := s.authorize(caller); err != nil {
		return Operation{}, err
	}
	var op Operation
	err := s.journal(ctx, func(j *journal) error {
		r, ok := j.Records[ref]
		h := sha256.Sum256([]byte(permit))
		if !ok || r.Owner != caller.User() || !reference.MatchString(permit) || r.PermitHash != hex.EncodeToString(h[:]) {
			return ErrRefused
		}
		if r.Acknowledged || r.Package != s.policy.Package || r.Version != s.policy.Version {
			return ErrRefused
		}
		allowed := phase == "unknown" && (r.State == "accepted" || r.State == "stopping" || r.State == "starting") || phase == "stopping" && r.State == "accepted" || phase == "starting" && r.State == "stopping"
		if !allowed {
			return ErrRefused
		}
		r.State = phase
		r.Updated = time.Now().UTC()
		j.Records[ref] = r
		op = r.Operation
		return nil
	})
	return op, err
}
