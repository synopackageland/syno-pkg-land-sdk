package background

import (
	"strconv"
	"time"
)

// Valid requires a resolved, non-root numeric UID. Unknown legacy owners remain
// visible only to administrators.
func (o Submitter) Valid() bool {
	uid, err := strconv.ParseUint(o.UID, 10, 32)
	return o.User != "" && err == nil && uid > 0 && strconv.FormatUint(uid, 10) == o.UID
}

// Access is constructed only by the authenticated backend, never JSON params.
type Access struct {
	Owner Submitter
	Admin bool
}

func (a Access) sees(job Job) bool {
	return !job.PurgeRequested && (a.Admin || (a.Owner.Valid() && job.Submitter == a.Owner))
}

type PurgeAudit struct {
	Owner    Submitter `json:"owner"`
	Actor    string    `json:"actor"`
	At       time.Time `json:"at"`
	Removed  int       `json:"removed"`
	Draining int       `json:"draining"`
}

// ownerUsage centralizes admission accounting for the package-private spool.
func (j *journal) ownerUsage(owner Submitter) (pending int, bytes int64) {
	for _, job := range j.Jobs {
		if job.Submitter != owner {
			continue
		}
		if !job.State.terminal() {
			pending++
			bytes += int64(job.InputBytes + resultReserve)
		} else {
			if job.CopyRetained {
				bytes += int64(job.InputBytes)
			}
			if job.Result != nil {
				bytes += resultCharge(job.Result)
			}
		}
	}
	for _, sc := range j.Schedules {
		if sc.Owner == owner {
			bytes += int64(sc.InputBytes)
		}
	}
	return
}
