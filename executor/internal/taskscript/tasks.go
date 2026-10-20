// Package taskscript renders the canonical lifecycle scripts shared with the CLI.
package taskscript

import (
	_ "embed"
	"errors"
	"regexp"
	"strings"
)

//go:embed boot.sh
var boot string

//go:embed control.sh
var control string

type Task struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Header string `json:"header"`
	Script string `json:"script"`
}

// Tasks pin only the approved policy. Executor bytes are anchored per SPK in the
// root-owned package INFO, so an executor-only upgrade keeps both definitions.
func Generate(pkg, pd string) ([]Task, error) {
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{1,63}$`).MatchString(pkg) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(pd) {
		return nil, errors.New("task pins")
	}
	replace := strings.NewReplacer("@PACKAGE@", pkg, "@POLICY_DIGEST@", pd)
	tasks := []Task{}
	for i, source := range []string{boot, control} {
		kind, limit := "boot", 1024
		if i == 1 {
			kind, limit = "control", 6000
		}
		script := replace.Replace(source)
		if len(script) > limit || strings.Contains(script, "%") {
			return nil, errors.New("task script")
		}
		tasks = append(tasks, Task{kind, pkg + " executor " + kind, "#!/bin/sh\n# synopkgland executor " + kind + "\n", script})
	}
	return tasks, nil
}
