// Package dsmservices reads the service definitions DSM's firewall and port
// forwarding use: INI files with one section per service, written by DSM
// itself (/usr/syno/etc/services.d, rewritten when the administrator changes
// a DSM port), by packages and by Container Manager
// (/usr/local/etc/services.d). The files are world-readable, so the package
// account reads them without privilege. A definition describes an installed
// service, not a running listener. Reading is tolerant: an odd line, value or
// file is skipped, never an error, and nothing is cached.
package dsmservices

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Dirs are DSM's own definitions, their factory defaults (read only for a
// file DSM's own directory lacks) and the packages' definitions.
var Dirs = []string{"/usr/syno/etc/services.d", "/usr/syno/etc.defaults/services.d", "/usr/local/etc/services.d"}

const (
	fileLimit  = 64 << 10
	fileCount  = 512
	portsLimit = 1024
)

// Definition is one section. Ports is the sorted single TCP-port projection
// used for web suggestions. Destinations retains TCP/UDP ranges too, without
// expanding them, for service naming and port-conflict checks.
type Definition struct {
	ID           string
	File         string
	Title        string
	TitleKey     string
	Desc         string
	DescKey      string
	Ports        []int
	Destinations []PortRange
}

// Read parses every *.sc file directly in dirs (not subdirectories such as
// tmp/). A file name seen in an earlier directory hides the same name in a
// later one, so DSM's live synow3.sc wins over its factory default.
func Read(dirs ...string) []Definition {
	if len(dirs) == 0 {
		dirs = Dirs
	}
	out := []Definition{}
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > fileCount {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".sc") || seen[name] || !entry.Type().IsRegular() {
				continue
			}
			raw, err := readFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			seen[name] = true
			out = append(out, Parse(name, raw)...)
		}
	}
	return out
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, fileLimit+1))
	if err == nil && len(raw) > fileLimit {
		return nil, os.ErrInvalid
	}
	return raw, err
}

// Parse reads one file. Values may be quoted or bare; dst.ports is
// "<ports>/<protocols>" where ports are comma-separated singles or ranges
// ("3240-3259", "3240:3259") and protocols are tcp, udp or all.
func Parse(file string, raw []byte) []Definition {
	out := []Definition{}
	var cur *Definition
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			id, ok := strings.CutSuffix(line[1:], "]")
			if id = strings.TrimSpace(id); !ok || id == "" {
				cur = nil
				continue
			}
			out = append(out, Definition{ID: id, File: file})
			cur = &out[len(out)-1]
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || cur == nil {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		value = strings.Map(func(r rune) rune {
			if r < ' ' || r == 0x7f {
				return -1
			}
			return r
		}, value)
		switch strings.TrimSpace(key) {
		case "title":
			cur.Title = value
		case "title_key":
			cur.TitleKey = value
		case "desc":
			cur.Desc = value
		case "desc_key":
			cur.DescKey = value
		case "dst.ports":
			cur.Ports = parsePorts(value)
			cur.Destinations = tolerantPortSpec(value)
		}
	}
	return out
}

func parsePorts(value string) []int {
	list, protocols, ok := strings.Cut(value, "/")
	if !ok || !hasTCP(protocols) {
		return nil
	}
	ports := []int{}
	seen := map[int]bool{}
	for _, token := range strings.Split(list, ",") {
		n, err := port(token)
		if err == nil && !seen[n] && len(ports) < portsLimit {
			seen[n] = true
			ports = append(ports, n)
		}
	}
	sort.Ints(ports)
	return ports
}

func hasTCP(protocols string) bool {
	for _, p := range strings.Split(protocols, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p == "tcp" || p == "all" {
			return true
		}
	}
	return false
}

func port(s string) (int, error) {
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil || s[0] < '0' || s[0] > '9' || n < 1 || n > 65535 {
		return 0, strconv.ErrRange
	}
	return n, nil
}
