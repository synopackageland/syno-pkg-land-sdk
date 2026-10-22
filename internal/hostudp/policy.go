// Package hostudp validates explicit package-UID host UDP listener exceptions.
package hostudp

import (
	"fmt"
	"regexp"
	"slices"
)

type Listener struct {
	Port    int    `json:"port"`
	Purpose string `json:"purpose"`
}

var purpose = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// Normalize returns a private, sorted inventory. Ports remain exact and UDP-only.
func Normalize(list []Listener) ([]Listener, error) {
	if len(list) > 32 {
		return nil, fmt.Errorf("host_udp allows at most 32 ports")
	}
	seen := map[int]bool{}
	for _, l := range list {
		if l.Port < 1024 || l.Port > 65535 || !purpose.MatchString(l.Purpose) || seen[l.Port] {
			return nil, fmt.Errorf("host_udp needs distinct ports 1024-65535 and lowercase purpose labels of 1-64 characters")
		}
		seen[l.Port] = true
	}
	if len(list) == 0 {
		return nil, nil
	}
	out := slices.Clone(list)
	slices.SortFunc(out, func(a, b Listener) int { return a.Port - b.Port })
	return out, nil
}
