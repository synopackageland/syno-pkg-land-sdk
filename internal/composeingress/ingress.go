// Package composeingress defines the fixed, package-scoped no-publish endpoint.
// It contains no Docker transport or administrative authority.
package composeingress

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

type Config struct {
	Candidate int `json:"candidate"`
}
type Allocation struct {
	Subnet  string `json:"subnet"`
	Address string `json:"address"`
}

// The private 10.128/9 pool is a candidate space, not a reserved network.
// A candidate is stable across builds, versions and resource-name prefixes.
func Allocate(pkg string, candidate int) (Allocation, error) {
	if candidate < 0 || candidate > 255 {
		return Allocation{}, fmt.Errorf("[COMPOSE_SUBNET_INVALID] ingress_candidate must be 0-255")
	}
	sum := sha256.Sum256([]byte(pkg + ":" + strconv.Itoa(candidate)))
	base := uint32(0x0a800000) | ((binary.BigEndian.Uint32(sum[:4]) & 0xfffff) << 3)
	bytes := [4]byte{}
	binary.BigEndian.PutUint32(bytes[:], base)
	addr := netip.AddrFrom4(bytes)
	prefix := netip.PrefixFrom(addr, 29)
	binary.BigEndian.PutUint32(bytes[:], base+2)
	return Allocation{prefix.String(), netip.AddrFrom4(bytes).String()}, nil
}

// CheckRoutes rejects overlap with every non-default IPv4 route in the kernel
// inventory. An upgrade may reuse only an exact previous package subnet.
// Docker IPAM remains the final atomic arbiter, including networks without routes.
func CheckRoutes(raw, subnet, previous string) error {
	wanted, err := netip.ParsePrefix(subnet)
	if err != nil {
		return err
	}
	scan := bufio.NewScanner(strings.NewReader(raw))
	if !scan.Scan() || !strings.HasPrefix(scan.Text(), "Iface") {
		return fmt.Errorf("[COMPOSE_SUBNET_INVENTORY_UNAVAILABLE] invalid kernel route inventory")
	}
	for scan.Scan() {
		f := strings.Fields(scan.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) < 8 {
			return fmt.Errorf("[COMPOSE_SUBNET_INVENTORY_UNAVAILABLE] malformed kernel route")
		}
		dest, e1 := strconv.ParseUint(f[1], 16, 32)
		mask, e2 := strconv.ParseUint(f[7], 16, 32)
		if e1 != nil || e2 != nil {
			return fmt.Errorf("[COMPOSE_SUBNET_INVENTORY_UNAVAILABLE] malformed IPv4 route")
		}
		if mask == 0 {
			continue
		}
		var d, m [4]byte
		binary.LittleEndian.PutUint32(d[:], uint32(dest))
		binary.LittleEndian.PutUint32(m[:], uint32(mask))
		bits := 0
		zero := false
		for _, b := range m {
			for bit := 7; bit >= 0; bit-- {
				if b&(1<<bit) != 0 {
					if zero {
						return fmt.Errorf("[COMPOSE_SUBNET_INVENTORY_UNAVAILABLE] noncontiguous route mask")
					}
					bits++
				} else {
					zero = true
				}
			}
		}
		p := netip.PrefixFrom(netip.AddrFrom4(d), bits).Masked()
		if !p.Overlaps(wanted) {
			continue
		}
		if p == wanted && previous == subnet && (strings.HasPrefix(f[0], "docker-") || strings.HasPrefix(f[0], "br-")) {
			continue
		}
		return fmt.Errorf("[COMPOSE_SUBNET_CONFLICT] ingress %s overlaps route %s on %s; choose another compose.network.ingress_candidate and rebuild", subnet, p, f[0])
	}
	if err := scan.Err(); err != nil {
		return fmt.Errorf("[COMPOSE_SUBNET_INVENTORY_UNAVAILABLE] %w", err)
	}
	return nil
}

// CheckAddresses also covers interface prefixes absent from the main route table.
func CheckAddresses(addresses map[string][]string, subnet, previous string) error {
	wanted, err := netip.ParsePrefix(subnet)
	if err != nil {
		return err
	}
	for iface, values := range addresses {
		for _, raw := range values {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil {
				return fmt.Errorf("[COMPOSE_SUBNET_INVENTORY_UNAVAILABLE] invalid interface prefix")
			}
			if !prefix.Addr().Is4() || !prefix.Overlaps(wanted) {
				continue
			}
			if prefix.Masked() == wanted && previous == subnet && (strings.HasPrefix(iface, "docker-") || strings.HasPrefix(iface, "br-")) {
				continue
			}
			return fmt.Errorf("[COMPOSE_SUBNET_CONFLICT] ingress %s overlaps interface %s on %s; choose another compose.network.ingress_candidate and rebuild", subnet, prefix, iface)
		}
	}
	return nil
}
