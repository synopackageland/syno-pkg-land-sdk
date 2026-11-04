package installsettings

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

const serviceTool = "/usr/syno/bin/servicetool"

// ErrPortCheck means service registration could not be checked. It deliberately
// does not wrap ErrPort: changing the selected port cannot repair a broken tool.
var ErrPortCheck = errors.New("INSTALL_PORT_CHECK_FAILED: retry installation; if this persists, ask the NAS administrator to check /usr/syno/bin/servicetool and DSM service registration")

// PortCheckError preserves command diagnostics separately from port conflicts.
// ExitCode is -1 when the tool could not start or was interrupted.
type PortCheckError struct {
	Reason   string
	ExitCode int
	Stderr   string
	Elapsed  time.Duration
	Cause    error
}

func (e *PortCheckError) Error() string {
	return fmt.Sprintf("%s (reason=%s exit_code=%d stderr=%s elapsed=%s cause=%v)", ErrPortCheck, e.Reason, e.ExitCode, strconv.QuoteToASCII(e.Stderr), e.Elapsed, e.Cause)
}

func (e *PortCheckError) Unwrap() error { return errors.Join(ErrPortCheck, e.Cause) }

// Keep draining the process pipes after the capture limit, so malformed tool
// output cannot grow memory or block the child before it exits.
type portToolOutput struct {
	raw       []byte
	truncated bool
}

func (b *portToolOutput) Write(p []byte) (int, error) {
	n := min(len(p), 16384-len(b.raw))
	b.raw = append(b.raw, p[:n]...)
	b.truncated = b.truncated || n < len(p)
	return len(p), nil
}

var portToolResponse = regexp.MustCompile(`^\s*IsConflict:\s*(true|false)\s+Port:\s*([0-9]+)\s+Protocol:\s*(tcp|udp)\s+ServiceName:\s*(\S+)\s*$`)

// CheckPort consults DSM's service definitions through a trusted absolute path.
// A loaded NAS may take arbitrarily long: only explicit caller cancellation or
// its deadline interrupts the command, never an SDK elapsed-time failure timer.
func CheckPort(ctx context.Context, pkg, key string, p Port, n int) error {
	return checkPort(ctx, serviceTool, pkg, key, p, n)
}

func checkPort(ctx context.Context, tool, pkg, key string, p Port, n int) error {
	started := time.Now()
	cmd := exec.CommandContext(ctx, tool, "--conf-port-conflict-check", "--"+p.Protocol, strconv.Itoa(n))
	var stdout, stderr portToolOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	elapsed := time.Since(started)
	failure := func(reason string, cause error) error {
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		summary := strings.Join(strings.Fields(string(stderr.raw)), " ")
		if len(summary) > 512 {
			summary = summary[:512] + "..."
		} else if stderr.truncated {
			summary += "..."
		}
		return &PortCheckError{Reason: reason, ExitCode: code, Stderr: summary, Elapsed: elapsed, Cause: cause}
	}
	if err != nil {
		return failure("exec", errors.Join(err, ctx.Err()))
	}
	match := portToolResponse.FindStringSubmatch(string(stdout.raw))
	if stdout.truncated || len(match) != 5 || match[2] != strconv.Itoa(n) || match[3] != p.Protocol {
		return failure("response", errors.New("unexpected service registry response"))
	}
	if match[1] == "false" || match[4] == portServiceID(pkg, key) {
		return nil
	}
	return ErrPort
}
