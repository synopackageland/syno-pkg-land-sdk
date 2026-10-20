// storage-init ships in the SPK and mounts read-only into the official image.
// It probes private subdirectories as the package UID, then replaces itself
// with the explicit application argv (no shell or privileged helper).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/compose"
)

func main() {
	declaration := flag.String("directories", "", "JSON writable-directory declarations")
	initialization := flag.String("initialization", "", "JSON storage and runtime initialization")
	flag.Parse()
	var err error
	if *initialization != "" {
		if *declaration != "" {
			err = fmt.Errorf("[COMPOSE_STORAGE_INIT_INVALID] choose one declaration")
		} else {
			err = runInitialization(*initialization, flag.Args())
		}
	} else {
		err = run(*declaration, flag.Args())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}

func run(raw string, argv []string) error {
	if len(raw) > 64<<10 || len(argv) == 0 {
		return fmt.Errorf("[COMPOSE_STORAGE_INIT_INVALID] declare directories and an application command after --")
	}
	var directories []compose.WritableDirectory
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&directories); err != nil {
		return fmt.Errorf("[COMPOSE_STORAGE_INIT_INVALID] %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("[COMPOSE_STORAGE_INIT_INVALID] expected one directory declaration")
	}
	return launch(compose.StorageInitialization{Directories: directories}, argv)
}

func runInitialization(raw string, argv []string) error {
	if len(raw) > 48<<10 || len(argv) == 0 {
		return fmt.Errorf("[COMPOSE_STORAGE_INIT_INVALID] invalid initialization or application argv")
	}
	var init compose.StorageInitialization
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&init); err != nil {
		return fmt.Errorf("[COMPOSE_STORAGE_INIT_INVALID] invalid initialization")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("[COMPOSE_STORAGE_INIT_INVALID] expected one initialization")
	}
	return launch(init, argv)
}

func launch(init compose.StorageInitialization, argv []string) error {
	seconds := compose.DefaultStartupTimeoutSeconds
	if raw := os.Getenv(compose.StartupBudgetEnv); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 2 || n > compose.MaxStartupTimeoutSeconds {
			return fmt.Errorf("[COMPOSE_STARTUP_INVALID] invalid startup budget")
		}
		seconds = n
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	syscall.Umask(0077)
	if err := compose.InitializeRuntime(ctx, init); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("[COMPOSE_RUNTIME_PREPARE_FAILED] startup canceled")
	}
	command, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("[COMPOSE_STORAGE_COMMAND_INVALID] %w", err)
	}
	// Values are derived from the running package account, not manifest input.
	os.Setenv("SYNOPKGLAND_UID", strconv.Itoa(os.Geteuid()))
	os.Setenv("SYNOPKGLAND_GID", strconv.Itoa(os.Getegid()))
	stop()
	if init.Runtime != nil && len(init.Runtime.Periodic) > 0 {
		appCtx, appStop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer appStop()
		return compose.RunWithPeriodic(appCtx, argv, init.Runtime.Periodic)
	}
	return syscall.Exec(command, argv, os.Environ())
}
