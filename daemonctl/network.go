package main

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeingress"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/hostudp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Host-network containers use the package UID. The kernel tables, rather than
// an application's claimed environment setting, prove its listener bindings.
// This is a configuration guard, not a network namespace or a firewall.
type networkPolicy struct {
	HostUDP     []hostudp.Listener     `json:"host_udp,omitempty"`
	Ingress     *composeingress.Config `json:"ingress,omitempty"`
	Readiness   *bridgeReadiness       `json:"readiness,omitempty"`
	Mode        string                 `json:"mode,omitempty"`
	Ports       []int                  `json:"ports"`
	PublicPorts []int                  `json:"public_ports"`
}

func containsPort(ports []int, port int) bool {
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

func checkSocketTable(raw string, uid int, p *networkPolicy, udp bool) error {
	_, err := socketPorts(raw, uid, p, udp)
	return err
}

func socketPorts(raw string, uid int, p *networkPolicy, udp bool) (map[int]bool, error) {
	found := map[int]bool{}
	scan := bufio.NewScanner(strings.NewReader(raw))
	if !scan.Scan() {
		return nil, errors.New("missing socket table header")
	}
	for scan.Scan() {
		f := strings.Fields(scan.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) < 10 {
			return nil, errors.New("invalid socket table")
		}
		owner, err := strconv.Atoi(f[7])
		if err != nil {
			return nil, err
		}
		if owner != uid {
			continue
		}
		if (!udp && f[3] != "0A") || (udp && f[3] != "07") {
			continue
		}
		endpoint := strings.Split(f[1], ":")
		if len(endpoint) != 2 {
			return nil, errors.New("invalid socket endpoint")
		}
		port64, err := strconv.ParseInt(endpoint[1], 16, 32)
		if err != nil {
			return nil, err
		}
		port := int(port64)
		if udp {
			allowed := false
			for _, listener := range p.HostUDP {
				if listener.Port == port {
					allowed = true
					break
				}
			}
			if allowed {
				continue
			}
		}
		if !udp && containsPort(p.PublicPorts, port) {
			continue
		}
		// /proc/net's IPv4 and IPv6 words are little-endian on supported DSM arches.
		loopback := endpoint[0] == "0100007F" || endpoint[0] == "00000000000000000000000001000000"
		if !loopback {
			return nil, fmt.Errorf("compose package listener on port %d is not bound to loopback", port)
		}
		if !udp && containsPort(p.Ports, port) && endpoint[0] == "0100007F" {
			found[port] = true
		}
	}
	return found, scan.Err()
}

func checkNetwork(dir string, uid int, p *networkPolicy) error {
	if p != nil && len(p.HostUDP) > 0 {
		if _, err := hostudp.Normalize(p.HostUDP); err != nil {
			return fmt.Errorf("COMPOSE_HOST_UDP_INVALID: %w", err)
		}
		if p.Mode != "host" {
			return errors.New("COMPOSE_HOST_UDP_INVALID: host_udp requires host mode")
		}
	}
	if p == nil || p.Mode == "settings" {
		return nil
	}
	if p.Mode == "bridge" {
		return errors.New("COMPOSE_READINESS_INVALID: bridge requires a versioned lifecycle readiness context")
	}
	if p.Mode != "" && p.Mode != "host" {
		return errors.New("unsupported network readiness profile")
	}
	found := map[int]bool{}
	for _, name := range []string{"tcp", "tcp6", "udp", "udp6"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		ports, err := socketPorts(string(raw), uid, p, strings.HasPrefix(name, "udp"))
		if err != nil {
			return err
		}
		for port := range ports {
			found[port] = true
		}
	}
	for _, port := range p.Ports {
		if !found[port] {
			return fmt.Errorf("compose upstream port %d is not ready as the package UID on 127.0.0.1", port)
		}
	}
	return nil
}
