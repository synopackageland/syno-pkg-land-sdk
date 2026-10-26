package dsmservices

import (
	"errors"
	"strconv"
	"strings"
)

// PortRange is an inclusive destination-port interval. TCP and UDP may both
// be true (all or tcp,udp). Ranges are never expanded by the SDK.
type PortRange struct {
	First int
	Last  int
	TCP   bool
	UDP   bool
}

// ErrInvalid denotes an invalid port declaration or incomplete configuration.
var ErrInvalid = errors.New("invalid DSM service declaration")

// ParsePortSpec parses dst.ports: comma-separated singles or inclusive ranges
// with - or :, followed by /tcp, /udp, /all or /tcp,udp. It rejects malformed
// declarations; Parse instead keeps each valid token of a mixed list.
func ParsePortSpec(value string) ([]PortRange, error) {
	list, protocols, ok := strings.Cut(value, "/")
	if !ok {
		return nil, ErrInvalid
	}
	tcp, udp := false, false
	for _, p := range strings.Split(protocols, ",") {
		switch strings.TrimSpace(p) {
		case "tcp":
			tcp = true
		case "udp":
			udp = true
		case "all":
			tcp, udp = true, true
		default:
			return nil, ErrInvalid
		}
	}
	out := []PortRange{}
	for _, token := range strings.Split(list, ",") {
		token = strings.TrimSpace(token)
		bounds := strings.FieldsFunc(token, func(r rune) bool { return r == ':' || r == '-' })
		if len(bounds) < 1 || len(bounds) > 2 {
			return nil, ErrInvalid
		}
		lo, err := strictPort(bounds[0])
		if err != nil {
			return nil, err
		}
		hi := lo
		if len(bounds) == 2 {
			hi, err = strictPort(bounds[1])
			if err != nil || hi < lo {
				return nil, ErrInvalid
			}
		}
		if token != bounds[0] && token != bounds[0]+":"+bounds[len(bounds)-1] && token != bounds[0]+"-"+bounds[len(bounds)-1] {
			return nil, ErrInvalid
		}
		out = append(out, PortRange{lo, hi, tcp, udp})
	}
	return out, nil
}

func strictPort(value string) (int, error) {
	if value == "" {
		return 0, ErrInvalid
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, ErrInvalid
		}
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 65535 {
		return 0, ErrInvalid
	}
	return n, nil
}

func tolerantPortSpec(value string) []PortRange {
	list, protocols, ok := strings.Cut(value, "/")
	if !ok {
		return nil
	}
	// Match the tolerant reader's case-insensitive protocol handling.
	protocols = strings.ToLower(protocols)
	var out []PortRange
	for _, token := range strings.Split(list, ",") {
		if ranges, err := ParsePortSpec(strings.TrimSpace(token) + "/" + protocols); err == nil {
			out = append(out, ranges...)
		}
	}
	return out
}
