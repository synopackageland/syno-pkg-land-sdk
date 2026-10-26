package dsmservices

import (
	"bufio"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ParseStrict reads destination declarations in file order, failing on malformed
// title quoting, port syntax, missing section or scanner overflow. Each dst.ports
// line produces one Definition with the title preceding that line; repeated
// lines remain separate. Titles must be printable, trimmed UTF-8, 1–80 runes.
// Use Parse for best-effort discovery and ParseStrict before following ports.
func ParseStrict(file string, raw []byte) ([]Definition, error) {
	out := []Definition{}
	section := ""
	title := ""
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			title = ""
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "dst.ports" && k != "title" {
			continue
		}
		// DSM writes quoted values; some packages (MailPlus Server) write bare
		// ones. Both are the same declaration; a half-quoted value is not.
		if strings.HasPrefix(v, `"`) || strings.HasSuffix(v, `"`) {
			if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
				return out, ErrInvalid
			}
			v = v[1 : len(v)-1]
		}
		if k == "title" {
			if validTitle(v) {
				title = v
			}
			continue
		}
		if section == "" {
			return out, ErrInvalid
		}
		ranges, err := ParsePortSpec(v)
		if err != nil {
			return out, err
		}
		out = append(out, Definition{ID: section, File: file, Title: title, Destinations: ranges})
	}
	if scanner.Err() != nil {
		return out, ErrInvalid
	}
	return out, nil
}

func validTitle(name string) bool {
	if !utf8.ValidString(name) || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
