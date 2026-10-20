// Command plan is the SDK's build-time Compose tool. It communicates through
// bounded JSON stdin/stdout, never through a shell or Docker socket.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/compose"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/containerpreflight"
	"syscall"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "diagnose" {
		if err := runDiagnosis(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "[COMPOSE_DIAGNOSIS_INVALID] Invalid diagnosis invocation")
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "preflight" {
		if err := containerpreflight.Check(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			// DSM's installer displays this package-account hook log when available.
			if path := os.Getenv("SYNOPKG_TEMP_LOGFILE"); path != "" {
				if f, e := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0600); e == nil {
					fmt.Fprintln(f, err)
					f.Close()
				}
			}
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "sources" {
		fs := flag.NewFlagSet("sources", flag.ExitOnError)
		root := fs.String("root", "", "source directory")
		file := fs.String("file", "", "source file")
		env := fs.String("env", "", "optional interpolation env")
		fs.Parse(os.Args[2:])
		var envs []string
		if *env != "" {
			envs = []string{*env}
		}
		inputs, err := compose.SnapshotInputs(*root, []string{*file}, envs)
		if err != nil {
			json.NewEncoder(os.Stdout).Encode(map[string]string{"error": err.Error()})
			os.Exit(1)
		}
		json.NewEncoder(os.Stdout).Encode(inputs)
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "prepare" || os.Args[1] == "remember-ingress") {
		fs := flag.NewFlagSet("prepare", flag.ExitOnError)
		project := fs.String("project", "", "incoming Compose directory")
		pkg := fs.String("package", "", "package id")
		pkgvar := fs.String("pkgvar", "", "DSM-owned package var")
		fs.Parse(os.Args[2:])
		operation := compose.Prepare
		if os.Args[1] == "remember-ingress" {
			operation = compose.RememberIngress
		}
		if err := operation(*project, *pkg, *pkgvar); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Stdin, os.Stdout); err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"error": err.Error()})
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer) error {
	var req compose.Request
	dec := json.NewDecoder(io.LimitReader(in, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return fmt.Errorf("[COMPOSE_REQUEST_INVALID] %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("[COMPOSE_REQUEST_INVALID] expected one JSON request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := compose.Build(ctx, req)
	if err != nil {
		return err
	}
	lock, err := p.LockJSON()
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		*compose.Plan
		Lock json.RawMessage `json:"lock"`
	}{p, lock})
}
