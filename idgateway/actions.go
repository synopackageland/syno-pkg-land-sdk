package idgateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DeleteAction is one step the gateway runs, as the package account, when a
// DSM account that signed in is deleted. Exactly one field is set. {uid} and
// {name} are the only placeholders.
type DeleteAction struct {
	// Remove deletes a path relative to the package var directory, which must
	// name the account with a placeholder. It never leaves that directory.
	Remove string `json:"remove,omitempty"`
	// Exec runs a program, by absolute path, without a shell; placeholders
	// expand inside single argv elements. It must succeed when the account is
	// already gone, since a failed deletion is retried from the first action.
	Exec []string `json:"exec,omitempty"`
}

// DeleteActionTimeout bounds one exec action.
const DeleteActionTimeout = time.Minute

var placeholderPattern = regexp.MustCompile(`\{[^{}]*\}`)

func checkPlaceholders(s string) error {
	for _, p := range placeholderPattern.FindAllString(s, -1) {
		if p != "{uid}" && p != "{name}" {
			return fmt.Errorf("unknown placeholder %s: only {uid} and {name}", p)
		}
	}
	return nil
}

func (a DeleteAction) validate() error {
	switch {
	case (a.Remove == "") == (len(a.Exec) == 0):
		return errors.New("a delete action has exactly one of remove and exec")
	case a.Remove != "":
		clean := filepath.Clean(a.Remove)
		if !filepath.IsLocal(a.Remove) || clean != a.Remove || strings.Split(clean, "/")[0] == "sso" {
			return fmt.Errorf("remove %q must be a clean path inside the var directory, outside sso/", a.Remove)
		}
		if !strings.Contains(a.Remove, "{uid}") && !strings.Contains(a.Remove, "{name}") {
			return fmt.Errorf("remove %q must name the account with {uid} or {name}", a.Remove)
		}
		return checkPlaceholders(a.Remove)
	default:
		if !filepath.IsAbs(a.Exec[0]) || placeholderPattern.MatchString(a.Exec[0]) {
			return fmt.Errorf("exec program %q must be an absolute path without placeholders", a.Exec[0])
		}
		for _, arg := range a.Exec[1:] {
			if err := checkPlaceholders(arg); err != nil {
				return err
			}
		}
		return nil
	}
}

// actionRunner runs delete actions in order and stops at the first failure.
type actionRunner struct {
	varDir  string
	timeout time.Duration
}

func (r actionRunner) run(ctx context.Context, actions []DeleteAction, s Subject) error {
	expand := strings.NewReplacer("{uid}", strconv.Itoa(s.UID), "{name}", s.Name).Replace
	for i, a := range actions {
		var err error
		if a.Remove != "" {
			err = r.remove(a.Remove, s, expand)
		} else {
			err = r.exec(ctx, a.Exec, s, expand)
		}
		if err != nil {
			return fmt.Errorf("on_delete[%d]: %w", i, err)
		}
	}
	return nil
}

func (r actionRunner) remove(pattern string, s Subject, expand func(string) string) error {
	if strings.Contains(pattern, "{name}") && (s.Name == "" || s.Name == "." || s.Name == ".." || strings.ContainsAny(s.Name, "/\x00")) {
		return fmt.Errorf("account name %q is not a path element", s.Name)
	}
	root, err := os.OpenRoot(r.varDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(expand(pattern))
}

func (r actionRunner) exec(ctx context.Context, argv []string, s Subject, expand func(string) string) error {
	timeout := r.timeout
	if timeout == 0 {
		timeout = DeleteActionTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := make([]string, len(argv)-1)
	for i, arg := range argv[1:] {
		args[i] = expand(arg)
	}
	cmd := exec.CommandContext(ctx, argv[0], args...)
	cmd.Env = append(os.Environ(), "SYNOPKGLAND_SSO_UID="+strconv.Itoa(s.UID), "SYNOPKGLAND_SSO_USER="+s.Name)
	out := &tailBuffer{max: 2048}
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = out, out, time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v: %s", filepath.Base(argv[0]), err, strings.TrimSpace(string(out.buf)))
	}
	return nil
}

// tailBuffer keeps the last max bytes written, for an error message.
type tailBuffer struct {
	buf []byte
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}
