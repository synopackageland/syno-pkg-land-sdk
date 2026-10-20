package background

import "context"

// Admission distinguishes a new/reused idempotent submission from an occupied
// owner-scoped singleton. Busy always carries the existing ID, never a new job.
type Admission struct {
	Outcome   string `json:"outcome"`
	Job       Job    `json:"job"`
	Duplicate bool   `json:"duplicate"`
}

// SubmitSingleton admits at most one pending job per owner and scope under the
// existing journal lock. Scope is a trusted backend operation/resource key, not
// an executable or a global lock. Paused jobs occupy their scope until canceled
// or finished. Use a fresh key after a completed run to start new work.
func (s *Store) SubmitSingleton(ctx context.Context, scope string, req SubmitRequest) (Admission, error) {
	if !keyRE.MatchString(scope) {
		return Admission{}, ErrInvalid
	}
	job, duplicate, err := s.submit(ctx, req, scope)
	if err != nil {
		return Admission{}, err
	}
	outcome := "accepted"
	if duplicate && !job.State.terminal() {
		outcome = "busy"
	}
	return Admission{Outcome: outcome, Job: job, Duplicate: duplicate}, nil
}
