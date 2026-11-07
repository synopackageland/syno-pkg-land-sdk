package ipc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
)

// RawTCPPolicyVersion identifies the build-bound raw TCP declaration contract.
const RawTCPPolicyVersion = 1

// Set by the packaging CLI, never taken from process environment or App files.
var tcpListenersPolicy string

var tcpListenerID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type tcpDeclaration struct {
	Scope     string `json:"scope,omitempty"`
	Port      int    `json:"port"`
	PortRange []int  `json:"port_range,omitempty"`
}

type tcpPolicy struct {
	Schema    int                       `json:"schema"`
	Listeners map[string]tcpDeclaration `json:"listeners"`
}

func readTCPPolicy(encoded string) (tcpPolicy, error) {
	var policy tcpPolicy
	if len(encoded) == 0 || len(encoded) > 32768 {
		return policy, errors.New("ipc: TCP listener policy is not bound by the build")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return policy, errors.New("ipc: invalid TCP listener policy encoding")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&policy); err != nil {
		return policy, fmt.Errorf("ipc: invalid TCP listener policy: %w", err)
	}
	if d.Decode(new(any)) != io.EOF || policy.Schema != RawTCPPolicyVersion || len(policy.Listeners) == 0 || len(policy.Listeners) > 64 {
		return policy, errors.New("ipc: invalid TCP listener policy schema or size")
	}
	for id, item := range policy.Listeners {
		if item.Scope == "all" {
			return policy, fmt.Errorf("ipc: TCP listener %q: scope \"all\" is not supported; listeners bind only loopback (127.0.0.1)", id)
		}
		if !tcpListenerID.MatchString(id) || item.Port < 1024 || item.Port > 65535 || (item.Scope != "" && item.Scope != "loopback") {
			return policy, errors.New("ipc: invalid TCP listener name, scope or port")
		}
		if item.PortRange != nil && (len(item.PortRange) != 2 || item.PortRange[0] < 1024 || item.PortRange[1] > 65535 || item.PortRange[0] > item.PortRange[1] || item.Port < item.PortRange[0] || item.Port > item.PortRange[1]) {
			return policy, errors.New("ipc: invalid TCP listener port_range")
		}
	}
	return policy, nil
}

// ListenTCP binds one manifest-declared raw IPv4 TCP listener. id must be a
// literal declared tcp_listeners ID at each audited call site. port=0 selects
// the declaration's default; another port must be explicitly allowed by its
// port_range (absent means only the default port). The listener binds exactly
// IPv4 127.0.0.1: scope is "loopback" or absent, and any other scope (such as
// "all") makes the policy invalid. No DNS, IPv6, non-loopback, ephemeral or
// privileged ports are accepted. A binary without the CLI's
// package identity and compiled declaration refuses the call.
//
// Cancellation closes admission and unblocks Accept; Close is idempotent.
// Accepted connections remain the caller's responsibility. This provides no
// DSM authentication, TLS, protocol validation or connection/rate/size limits.
// Local processes can connect to loopback; authenticate every SMTP/protocol
// peer and bound sessions in the App. This is not an HTTP registry exemption.
func ListenTCP(ctx context.Context, id string, port int) (net.Listener, error) {
	if !packageName.MatchString(packageID) {
		return nil, errors.New("ipc: package identity is not bound by the build")
	}
	policy, err := readTCPPolicy(tcpListenersPolicy)
	if err != nil {
		return nil, err
	}
	item, exists := policy.Listeners[id]
	if !exists {
		return nil, fmt.Errorf("ipc: undeclared TCP listener %q", id)
	}
	if port == 0 {
		port = item.Port
	}
	minPort, maxPort := item.Port, item.Port
	if item.PortRange != nil {
		minPort, maxPort = item.PortRange[0], item.PortRange[1]
	}
	if port < minPort || port > maxPort {
		return nil, fmt.Errorf("ipc: TCP listener %q port is outside its declared range", id)
	}
	// Reuse the reservation bind primitive, retaining the very same socket:
	// no release/rebind window in which another process can steal the port.
	raw, err := listenLoopback(ctx, uint16(port))
	if err != nil {
		return nil, err
	}
	l := &tcpListener{Listener: raw}
	l.stop = context.AfterFunc(ctx, func() { _ = l.closeRaw() })
	if err := ctx.Err(); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

type tcpListener struct {
	net.Listener
	stop func() bool
	once sync.Once
	err  error
}

func (l *tcpListener) closeRaw() error {
	l.once.Do(func() { l.err = l.Listener.Close() })
	return l.err
}

func (l *tcpListener) Close() error {
	l.stop()
	return l.closeRaw()
}
