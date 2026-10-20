package dsm

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// IsAdministrator is the single DSM role provider. The native SDK owns group
// names, domain/LDAP membership and real-time checks. Unknown results refuse.
func IsAdministrator(ctx context.Context, principal string) (bool, error) {
	if !validPrincipal(principal) {
		return false, errors.New("invalid DSM principal")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return nativeAdministrator(ctx, principal)
}

var errNativeRole = errors.New("DSM administrator verification unavailable")
var errOutputLimit = errors.New("DSM authentication output limit exceeded")

type boundedOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errOutputLimit
	}
	return b.buffer.Write(p)
}

func (b *boundedOutput) Len() int      { return b.buffer.Len() }
func (b *boundedOutput) Bytes() []byte { return b.buffer.Bytes() }

func commandOutput(cmd *exec.Cmd, limit int) ([]byte, error) {
	var out = boundedOutput{limit: limit}
	var stderr = boundedOutput{limit: 1024}
	cmd.Stdout, cmd.Stderr = &out, &stderr
	cmd.WaitDelay = 250 * time.Millisecond
	configureCommand(cmd)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func runCGI(ctx context.Context, path string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path)
	cmd.Env = env
	return commandOutput(cmd, 1026)
}

func parseNativeRole(out []byte, err error) (bool, error) {
	return parseNativeBit(out, err, errNativeRole)
}

// parseNativeBit accepts only a bridge's exact "1\n" or "0\n"; anything else,
// including a failed run, is the caller's unavailable error.
func parseNativeBit(out []byte, err, unavailable error) (bool, error) {
	if err != nil {
		return false, unavailable
	}
	switch string(out) {
	case "1\n":
		return true, nil
	case "0\n":
		return false, nil
	default:
		return false, unavailable
	}
}
