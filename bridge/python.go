//go:build unix

package bridge

import (
	"context"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"net"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// MinPython is the oldest Python the SDK supports.
var MinPython = [2]int{3, 8}

// pythonName is an interpreter whose file name says its version, as DSM's
// /usr/bin/python3 -> python3.8 does.
var pythonName = regexp.MustCompile(`^python([0-9]+)\.([0-9]+)$`)

// pythonExec starts a python App after checking the interpreter is new
// enough; an older one leaves the App unavailable, with why in the log.
type pythonExec struct{ *Exec }

func (p pythonExec) Dial(ctx context.Context) (net.Conn, error) {
	probeCtx := ctx
	cancel := func() {}
	if until, ok := ctx.Value(startupDeadlineKey{}).(time.Time); ok {
		probeCtx, cancel = context.WithDeadline(ctx, until)
	}
	defer cancel()
	major, minor, err := pythonVersion(probeCtx, p.Path)
	if err != nil {
		return nil, fmt.Errorf("bridge: cannot tell the version of %s: %w", p.Path, err)
	}
	if major < MinPython[0] || major == MinPython[0] && minor < MinPython[1] {
		return nil, fmt.Errorf("bridge: %s is Python %d.%d; the SDK needs %d.%d or newer", p.Path, major, minor, MinPython[0], MinPython[1])
	}
	return p.Exec.Dial(ctx)
}

// pythonVersion reads the version from the interpreter's file name, or asks
// the interpreter when the name does not say.
func pythonVersion(ctx context.Context, path string) (int, int, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		if m := pythonName.FindStringSubmatch(filepath.Base(real)); m != nil {
			return atoi2(m[1], m[2])
		}
	}
	ctx, cancel := context.WithTimeout(ctx, runtimebudget.Inner(runtimebudget.Remaining(ctx, runtimebudget.Operation)/2))
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-I", "-S", "-c", "import sys; print(*sys.version_info[:2])")
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	var major, minor int
	if _, err := fmt.Sscanf(string(out), "%d %d\n", &major, &minor); err != nil {
		return 0, 0, fmt.Errorf("unexpected answer %q", out)
	}
	return major, minor, nil
}

func atoi2(a, b string) (int, int, error) {
	x, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, err
	}
	y, err := strconv.Atoi(b)
	return x, y, err
}
