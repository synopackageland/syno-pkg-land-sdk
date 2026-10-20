//go:build unix

// Command runtime is the package CGI (ui/cgi/api.cgi) for an App written in
// Python, Node or Rust; spec/bridge/v1 describes the App directory it reads.
// The package build binds the package ID and the digests of bridge.json and
// permissions.json:
//
//	go build -ldflags "-X main.packageID=<package> -X main.manifestDigest=<sha256> -X main.permissionsDigest=<sha256>" ./bridge/cmd/runtime
//
// For an executor-managed Application Portal entry (App targets DSM 7.1),
// the build also binds -X main.portalMode=executor. This enables the fixed
// administrator portal.register/unregister routes and status.entry.
//
// With --worker it is the package's background worker instead: DSM's
// systemd user unit runs it as the package account, and it runs the jobs of
// the capabilities jobs and delegate until SIGTERM.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/cgi"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"github.com/synopackageland/syno-pkg-land-sdk/bridge"
	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
)

var packageID, manifestDigest, permissionsDigest, portalMode string

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--worker" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		if err := work(ctx, os.Getenv, os.Geteuid()); err != nil {
			log.Fatal(err)
		}
		return
	}
	h, err := runtime(os.Getenv, os.Geteuid())
	if err != nil {
		log.Fatal(err)
	}
	if err := cgi.Serve(h); err != nil {
		log.Fatal(err)
	}
}

// runtime checks how the command was started and builds the handler.
func runtime(getenv func(string) string, uid int) (http.Handler, error) {
	if getenv("GATEWAY_INTERFACE") == "" {
		return nil, errors.New("runtime: run by DSM's web server as the package CGI")
	}
	if manifestDigest == "" || permissionsDigest == "" {
		return nil, errors.New("runtime: built without the digests of bridge.json and permissions.json")
	}
	if err := background.RefuseRoot(uid); err != nil {
		return nil, errors.New("runtime: refuses to run as root")
	}
	dsm.SetBridgeFallbackDir("/var/packages/" + packageID + "/tmp")
	return bridge.NewRuntime(cgiConfig(getenv))
}

// cgiConfig is the runtime of the package build's bound values; of the
// request it reads only SCRIPT_NAME.
func cgiConfig(getenv func(string) string) bridge.Config {
	return bridge.Config{
		PackageID:         packageID,
		PortalExecutor:    portalMode == "executor",
		ManifestDigest:    manifestDigest,
		PermissionsDigest: permissionsDigest,
		ScriptName:        getenv("SCRIPT_NAME"),
	}
}

// work runs the background worker until ctx ends.
func work(ctx context.Context, getenv func(string) string, uid int) error {
	if getenv("GATEWAY_INTERFACE") != "" {
		return errors.New("runtime: --worker is run by the package's systemd user unit, not by the web server")
	}
	if manifestDigest == "" || permissionsDigest == "" {
		return errors.New("runtime: built without the digests of bridge.json and permissions.json")
	}
	if err := background.RefuseRoot(uid); err != nil {
		return errors.New("runtime: refuses to run as root")
	}
	info := background.WorkerInfo{PID: os.Getpid(), UID: uid}
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		info.Username = u.Username
	}
	log.SetPrefix(packageID + " background: ")
	return bridge.Worker(ctx, workerConfig(), info)
}

// workerConfig is the worker of the package build's bound values.
func workerConfig() bridge.Config {
	return bridge.Config{PackageID: packageID, ManifestDigest: manifestDigest}
}
