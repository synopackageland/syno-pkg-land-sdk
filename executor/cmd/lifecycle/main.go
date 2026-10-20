// This helper runs as the package account, separately from the root executor.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/budget"
	"os"
	"path/filepath"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/lifecycle"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "executor lifecycle:", err)
		os.Exit(1)
	}
}

type request struct {
	action string
	cfg    lifecycle.Config
	wait   time.Duration
}

func parse(args []string) (request, error) {
	if len(args) == 0 {
		return request{}, errors.New("start, stop or purge required")
	}
	r := request{action: args[0], wait: waitBudget(budget.Default(), args[0])}
	fs := flag.NewFlagSet("lifecycle", flag.ContinueOnError)
	pkg := fs.String("package", "", "package account")
	deleteState := fs.Bool("delete-state", false, "the administrator asked to delete the App's state (purge only)")
	upgrade := fs.Bool("upgrade", false, "the stop is for a package upgrade (stop only)")
	if err := fs.Parse(args[1:]); err != nil {
		return request{}, err
	}
	if fs.NArg() != 0 {
		return request{}, errors.New("unexpected arguments")
	}
	if *deleteState && r.action != "purge" {
		return request{}, errors.New("--delete-state is for purge only")
	}
	if *upgrade && r.action != "stop" {
		return request{}, errors.New("--upgrade is for stop only")
	}
	// The package copy controls only how long this unprivileged helper waits.
	// Root independently reads its protected, digest-approved copy.
	if exe, err := os.Executable(); err == nil {
		if raw, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "policy.json")); err == nil && len(raw) <= 1<<20 {
			var policy struct {
				Timeouts *budget.Policy `json:"timeouts"`
			}
			if json.Unmarshal(raw, &policy) == nil && policy.Timeouts != nil && policy.Timeouts.Valid() {
				r.wait = waitBudget(*policy.Timeouts, r.action)
			}
		}
	}
	data := os.Getenv("SYNOPKG_PKGVAR")
	if data == "" {
		data = filepath.Join("/var/packages", *pkg, "var")
	}
	r.cfg = lifecycle.Config{Package: *pkg, Data: data, Socket: filepath.Join("/run/synopkgland", *pkg, "executor.sock"), DeleteState: *deleteState, Upgrade: *upgrade, Report: func(state string) { fmt.Printf("{\"accepted\":true,\"state\":%q}\n", state) }, Log: func(s string) { fmt.Fprintln(os.Stderr, s) }}
	return r, nil
}

func run(args []string) error {
	r, err := parse(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.wait)
	defer cancel()
	return lifecycle.Run(ctx, r.cfg, r.action)
}

func waitBudget(p budget.Policy, action string) time.Duration {
	if action == "purge" {
		return p.Duration("stop") + p.Duration("purge")
	}
	return p.Duration(action)
}
