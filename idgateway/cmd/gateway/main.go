//go:build unix

// Command gateway is the DSM identity gateway of a package. The package build
// binds its configuration path:
//
//	go build -ldflags "-X main.configPath=/var/packages/<package>/target/sso/gateway.json" ./idgateway/cmd/gateway
//
// Run by DSM as a CGI (ui/sso/authorize.cgi) it is the authorize CGI. As the
// package daemon it is the gateway:
//
//	gateway run [--config <path>] [-- <app> <args>...]
//
// With an app command it supervises the app, passing the provider issuer
// and client secrets in SYNOPKGLAND_SSO_* variables, and exits when it does.
// "gateway exec -- <app> <args>..." runs the app alone, for its other hooks
// such as uninstall.
//
// With an account section in the configuration, run as the package account:
//
//	gateway account status [--json] [--config <path>]
//	gateway account purge <uid> [--config <path>]
//
// status lists pending deletions and domain accounts DSM can no longer find;
// purge deletes one of those.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cgi"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
	"github.com/synopackageland/syno-pkg-land-sdk/idgateway"
)

var configPath string

func main() {
	if os.Getenv("GATEWAY_INTERFACE") != "" {
		h, err := authorizeHandler(configPath)
		if err != nil {
			log.Fatal(err)
		}
		if err := cgi.Serve(h); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func authorizeHandler(path string) (http.Handler, error) {
	cfg, err := idgateway.LoadConfig(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(os.Getenv("SCRIPT_NAME"), "/open.cgi") {
		return idgateway.EntryHandler(cfg), nil
	}
	key, err := idgateway.GatewayKey(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	var ports []int
	for _, p := range []int{cfg.Listen.HTTP, cfg.Listen.HTTPS} {
		if p != 0 {
			ports = append(ports, p)
		}
	}
	verifier, err := dsm.NewEntryGate(dsm.NewAuthenticator(), cfg.Entry, cfg.Package, cfg.StateDir)
	if err != nil {
		return nil, err
	}
	access := idgateway.AccessVerifier{Next: verifier, Access: cfg.Access, Groups: cfg.Groups, Membership: idgateway.NewDirectory()}
	loginApp := ""
	if cfg.Mode == idgateway.ModeOIDC && cfg.OIDC != nil && (cfg.OIDC.NativeLogin || cfg.OIDC.Browser != nil) {
		loginApp = cfg.Package
	}
	return &idgateway.AuthorizeCGI{LoginApp: loginApp, Key: key, AppPorts: ports, Verifier: access, Accounts: idgateway.NewDirectory(), Now: time.Now}, nil
}

const usage = "usage: gateway run [--config <path>] [-- <app> <args>...] | gateway exec -- <app> <args>... | gateway account status|purge"

func run(args []string) error {
	if len(args) > 2 && args[0] == "exec" && args[1] == "--" {
		return syscall.Exec(args[2], args[2:], os.Environ())
	}
	if len(args) > 0 && args[0] == "account" {
		return account(args[1:], os.Stdout)
	}
	if len(args) == 0 || args[0] != "run" {
		return errors.New(usage)
	}
	args, path := args[1:], configPath
	var app []string
	if i := slices.Index(args, "--"); i >= 0 {
		args, app = args[:i], args[i+1:]
	}
	switch {
	case len(args) == 2 && args[0] == "--config":
		path = args[1]
	case len(args) != 0:
		return fmt.Errorf("unexpected arguments %q", args)
	}
	cfg, err := idgateway.LoadConfig(path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return idgateway.Run(ctx, cfg, idgateway.ReadDSMPorts("/etc/synoinfo.conf"), app)
}

const accountUsage = "usage: gateway account status [--json] [--config <path>] | gateway account purge <uid> [--config <path>]"

func account(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(accountUsage)
	}
	verb, path, asJSON := args[0], configPath, false
	var rest []string
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--json" && verb == "status":
			asJSON = true
		case args[i] == "--config" && i+1 < len(args):
			path = args[i+1]
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	var uid int
	switch {
	case verb == "status" && len(rest) == 0:
	case verb == "purge" && len(rest) == 1:
		var err error
		if uid, err = strconv.Atoi(rest[0]); err != nil {
			return fmt.Errorf("purge needs a uid, not %q", rest[0])
		}
	default:
		return errors.New(accountUsage)
	}
	cfg, err := idgateway.LoadConfig(path)
	if err != nil {
		return err
	}
	d := cfg.Deletions()
	if d == nil {
		return fmt.Errorf("%s has no account section: account deletions are off", path)
	}
	ctx := context.Background()
	if verb == "purge" {
		return d.Purge(ctx, uid)
	}
	st, err := d.Status(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(out).Encode(st)
	}
	printStatus(out, st)
	return nil
}

func printStatus(out io.Writer, st idgateway.AccountStatus) {
	if len(st.Pending) == 0 {
		fmt.Fprintln(out, "no pending deletions")
	} else {
		fmt.Fprintln(out, "pending deletions (the running gateway retries them, waiting up to 5 minutes between attempts):")
	}
	for _, p := range st.Pending {
		fmt.Fprintf(out, "  uid %d (%s) since %s, %d attempts: %s\n", p.UID, p.Name, p.Since.Format(time.RFC3339), p.Attempts, p.Error)
	}
	if len(st.Missing) == 0 {
		fmt.Fprintln(out, "no app accounts whose DSM user can no longer be found")
		return
	}
	fmt.Fprintln(out, "app accounts whose DSM user can no longer be found (domain accounts; delete with: gateway account purge <uid>):")
	for _, m := range st.Missing {
		fmt.Fprintf(out, "  uid %d (%s)\n", m.UID, m.Name)
	}
}
