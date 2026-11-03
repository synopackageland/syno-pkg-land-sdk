// Package budget holds work bounds pinned by the approved policy. It never
// changes session, token, grant or approval lifetimes.
package budget

import (
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"time"
)

const Operation = runtimebudget.Operation
const MaxSeconds = runtimebudget.MaxSeconds
const Reserve = 5 * time.Second

type Policy struct {
	Start     int `json:"start"`
	Stop      int `json:"stop"`
	Purge     int `json:"purge"`
	Operation int `json:"operation"`
}

func Default() Policy {
	n := int(Operation / time.Second)
	return Policy{Start: n, Stop: n, Purge: n, Operation: n}
}
func (p Policy) Valid() bool {
	for _, n := range []int{p.Start, p.Stop, p.Purge} {
		if n < 2 || n > 3600 {
			return false
		}
	}
	return p.Operation >= 1 && p.Operation <= 3600
}
func (p Policy) Duration(action string) time.Duration {
	n := p.Operation
	switch action {
	case "start":
		n = p.Start
	case "stop":
		n = p.Stop
	case "purge":
		n = p.Purge
	}
	return time.Duration(n) * time.Second
}
func Inner(d time.Duration) time.Duration { return max(0, d-min(Reserve, d/4)) }
