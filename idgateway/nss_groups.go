package idgateway

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxDirectoryOutput = 64 << 10
const maxDirectoryGroups = 256

// DSM's dynamic /usr/bin/id uses libc NSS; os/user in the static CGI does not.
// A fixed executable, a single validated username argument and a clean locale
// environment keep the browser's credentials and process environment out of NSS.
// No SDK cache: query once per authenticated request, with a two-second limit.
type directoryLookup struct {
	timeout time.Duration
	run     func(context.Context, string, []string, []string) ([]byte, error)
}

func (d directoryLookup) groups(ctx context.Context, user string) ([]string, error) {
	if !userName.MatchString(user) || strings.HasPrefix(user, "-") {
		return nil, errors.New("invalid directory account")
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	out, err := d.run(ctx, "/usr/bin/id", []string{user}, []string{"LC_ALL=C"})
	if ctx.Err() != nil {
		return nil, fmt.Errorf("directory groups: %w", ctx.Err())
	}
	if err != nil {
		return nil, errors.New("directory group lookup failed")
	}
	return parseDirectoryGroups(out)
}

// Bounded stdout also bounds memory if a directory returns an excessive list.
// Never include command output in errors: it is not a diagnostic credential path.
type directoryOutput struct{ data []byte }

func (b *directoryOutput) Write(p []byte) (int, error) {
	if len(p) > maxDirectoryOutput-len(b.data) {
		return 0, errors.New("directory output exceeds limit")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func runDirectoryCommand(ctx context.Context, path string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.WaitDelay = 100 * time.Millisecond
	var out directoryOutput
	cmd.Stdout = &out
	err := cmd.Run()
	return out.data, err
}

var directoryIDLine = regexp.MustCompile(`^uid=([0-9]+)\(([^()\r\n]+)\) gid=([0-9]+)\(([^()\r\n]+)\) groups=([^\r\n]+)\n?$`)
var directoryGroupEntry = regexp.MustCompile(`^([0-9]+)\(([^()\r\n]+)\)$`)

// Parse full id output, not id -Gn: the latter loses boundaries when AD group
// names contain spaces (for example EXAMPLE\Domain Users). Require every gid to
// resolve to a bounded valid name, and require the primary group in the list.
// Unresolved numeric gids, partial/malformed output and ambiguous delimiters deny.
func parseDirectoryGroups(out []byte) ([]string, error) {
	invalid := errors.New("incomplete or invalid directory groups")
	if len(out) > maxDirectoryOutput {
		return nil, invalid
	}
	m := directoryIDLine.FindStringSubmatch(string(out))
	if m == nil || !validDirectoryID(m[1]) || !validDirectoryID(m[3]) || !userName.MatchString(m[2]) || !groupName.MatchString(m[4]) {
		return nil, invalid
	}
	entries := strings.Split(m[5], ",")
	if len(entries) > maxDirectoryGroups {
		return nil, invalid
	}
	groups := make([]string, 0, len(entries))
	seen := map[string]bool{}
	primary := false
	for _, entry := range entries {
		g := directoryGroupEntry.FindStringSubmatch(entry)
		if g == nil || !validDirectoryID(g[1]) || !groupName.MatchString(g[2]) || seen[g[1]] {
			return nil, invalid
		}
		seen[g[1]] = true
		if g[1] == m[3] {
			if g[2] != m[4] {
				return nil, invalid
			}
			primary = true
		}
		groups = append(groups, g[2])
	}
	if !primary {
		return nil, invalid
	}
	return groups, nil
}
func validDirectoryID(value string) bool {
	n, err := strconv.ParseUint(value, 10, 32)
	return err == nil && n < 1<<32-1 && strconv.FormatUint(n, 10) == value
}

var userName = regexp.MustCompile(`^[A-Za-z0-9._@\\-]{1,193}$`)
var groupName = regexp.MustCompile(`^(?:[A-Za-z0-9._-]{1,64}|[A-Za-z0-9._-]{1,64}\\[A-Za-z0-9._-](?:[A-Za-z0-9._ -]{0,126}[A-Za-z0-9._-])?|[A-Za-z0-9._-](?:[A-Za-z0-9._ -]{0,126}[A-Za-z0-9._-])?@[A-Za-z0-9._-]{1,64})$`)

// Groups uses DSM's NSS-aware id, including qualified group names with spaces.
func (d *Directory) Groups(ctx context.Context, name string) ([]string, error) {
	return (directoryLookup{timeout: 2 * time.Second, run: runDirectoryCommand}).groups(ctx, name)
}
