package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// leftoverPaths are where DSM keeps the package's resource declaration and
// the Login Portal files its web-config worker writes and releases.
// Entry is where the executor's own portal entry lives.
type leftoverPaths struct {
	Packages, AppPortal, NginxConf string
	Entry                          portalPaths
}

var dsmLeftovers = leftoverPaths{Packages: "/var/packages", AppPortal: "/usr/syno/etc/www/app.d", NginxConf: "/etc/nginx/conf.d", Entry: dsmPortal}

// purgeLeftovers removes what a DSM uninstall keeps for any package: the
// Application Privileges rules of the App, and the Login Portal alias nginx
// still serves after DSM released the web-config. Failures are only logged.
func purgeLeftovers(ctx context.Context, d *liveDSM, pkg string, paths leftoverPaths, log func(string)) error {
	first := purgeAppPrivileges(ctx, d, pkg, log)
	second := reloadPortal(ctx, d, pkg, paths, log)
	return errors.Join(first, second)
}

// appPrivDeleteTimeout bounds the rule delete: DSM runs every package's
// app_privilege_set hooks inside it, which took 16-20 seconds on nas.example.
const appPrivDeleteTimeout = 90 * time.Second

type appPrivRule struct {
	App  string `json:"app_id"`
	Name string `json:"entity_name"`
	Type string `json:"entity_type"`
}

// purgeAppPrivileges deletes the rules of exactly SYNO.SDS.<pkg>.Instance
// through the Application Privileges API Control Panel uses.
func purgeAppPrivileges(ctx context.Context, d *liveDSM, pkg string, log func(string)) error {
	app := "SYNO.SDS." + pkg + ".Instance"
	rules, err := listAppPrivRules(ctx, d, app)
	if err != nil {
		log("app privilege cleanup skipped: rule list unavailable: app=" + app)
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	// A failed delete call can still have deleted: the rules left decide.
	deleteCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		deleteCtx = withCommandTimeout(ctx, appPrivDeleteTimeout)
	}
	deleteErr := d.mutate(deleteCtx, "SYNO.Core.AppPriv.Rule", "delete", "1", map[string]any{"rules": rules})
	reason := ""
	if deleteErr != nil {
		reason = " delete_error=" + failureReason(deleteErr)
	}
	left, err := listAppPrivRules(ctx, d, app)
	switch {
	case err != nil:
		log("app privilege cleanup unconfirmed: app=" + app + reason)
	case len(left) != 0:
		log(fmt.Sprintf("app privilege cleanup failed: app=%s rules_left=%d%s", app, len(left), reason))
	default:
		log(fmt.Sprintf("app privilege cleanup: app=%s rules=%d%s", app, len(rules), reason))
	}
	if err != nil || len(left) != 0 {
		return errors.New("app privilege cleanup pending")
	}
	return nil
}

// failureReason names why a DSM call failed, for the journal.
func failureReason(err error) string {
	var refused *dsmError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &refused):
		return fmt.Sprintf("dsm-%d", refused.Code)
	}
	return "command"
}

func listAppPrivRules(ctx context.Context, d *liveDSM, app string) ([]appPrivRule, error) {
	data, err := d.call(ctx, "SYNO.Core.AppPriv.Rule", "list", "1", "", map[string]any{"app_id": app})
	if err != nil {
		return nil, err
	}
	var out struct {
		Rules []appPrivRule `json:"rules"`
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	var own []appPrivRule
	for _, r := range out.Rules {
		if r.App == app && r.Type != "" && r.Name != "" {
			own = append(own, r)
		}
	}
	return own, nil
}

// reloadPortal reloads nginx the way nginx.service does, once DSM has
// released the package's Login Portal files: DSM removes them on uninstall
// but leaves the running nginx serving the alias.
func reloadPortal(ctx context.Context, d *liveDSM, pkg string, paths leftoverPaths, log func(string)) error {
	declared, err := portalDeclared(filepath.Join(paths.Packages, pkg, "conf", "resource"))
	if err != nil {
		log("portal reload skipped: portal declaration unavailable: package=" + pkg)
		return err
	}
	if !declared {
		return nil
	}
	for _, file := range []string{
		filepath.Join(paths.AppPortal, pkg+".json"),
		filepath.Join(paths.NginxConf, "alias.syno-app-portal."+pkg+".conf"),
		filepath.Join(paths.NginxConf, ".alias.syno-app-portal."+pkg+".conf"),
		filepath.Join(paths.NginxConf, "dsm.syno-app-portal."+pkg+".conf"),
	} {
		if _, err := os.Lstat(file); !os.IsNotExist(err) {
			log("portal reload skipped: portal still registered: package=" + pkg)
			return errors.New("portal cleanup pending")
		}
	}
	if _, err := d.Command.Exec(ctx, "/usr/syno/bin/synosystemctl", []string{"reload", "nginx"}); err != nil {
		log("portal cleanup: nginx reload failed: package=" + pkg)
		return err
	}
	log("portal cleanup: nginx reloaded: package=" + pkg)
	return nil
}

func portalDeclared(resource string) (bool, error) {
	// The declaration only decides whether to reload nginx, which DSM itself
	// does on every alias change; it grants nothing.
	f, err := os.Open(resource)
	if err != nil {
		return false, err
	}
	defer f.Close()
	b, err := readLimited(f, 65536)
	if err != nil {
		return false, err
	}
	var r struct {
		WebConfig struct {
			AppPortal json.RawMessage `json:"app-portal"`
		} `json:"web-config"`
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return false, err
	}
	return len(r.WebConfig.AppPortal) > 0, nil
}

// purgePortalEntry removes the Application Portal entry the approved policy
// let the executor register: DSM knows nothing of it and keeps it after any
// uninstall. Without a portal in the policy the portal files are DSM's.
func purgePortalEntry(ctx context.Context, d *liveDSM, pkg, state, runtime string, paths portalPaths, log func(string)) error {
	policy, err := runtimePolicy(pkg, runtime)
	if err != nil {
		log("portal entry cleanup skipped: policy unavailable: package=" + pkg)
		return err
	}
	if policy.Portal == nil {
		return nil
	}
	// Purge removes state afterwards; its marker only has to outlive the steps.
	entry := portalEntry{Package: pkg, Policy: *policy.Portal, Paths: paths, DSM: d, StateDir: state, OperationBudget: policy.budgets().Duration("operation")}
	if err = entry.Unregister(ctx); err != nil {
		log("portal entry cleanup failed: package=" + pkg)
		return err
	}
	log("portal entry cleanup: package=" + pkg)
	return nil
}

func runtimePolicy(pkg, runtime string) (Policy, error) {
	f, err := openRegular(filepath.Join(runtime, "policy.json"))
	if err != nil {
		return Policy{}, err
	}
	defer f.Close()
	b, err := readLimited(f, maxRequest)
	if err != nil {
		return Policy{}, err
	}
	var p Policy
	if err = strictJSON(b, &p); err != nil {
		return Policy{}, err
	}
	return p, validatePolicy(p, pkg)
}
