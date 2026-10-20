package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// leftoverDSM answers the AppPriv rule list and delete DSM uses in Control
// Panel > Application Privileges, and records an nginx reload.
type leftoverDSM struct {
	rules     []map[string]any
	listErr   bool
	deleteErr bool
	// deleteLate is the error of a delete whose rules DSM still removed,
	// such as the 16-20 seconds nas.example takes (the regression test bug 1).
	deleteLate error
	// deleteBudget is how long the delete call was given.
	deleteBudget time.Duration
	reloadErr    error
	deleted      []string
	reloads      int
}

func (s *leftoverDSM) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	if path == "/usr/syno/bin/synosystemctl" {
		if strings.Join(args, " ") != "reload nginx" {
			return nil, errors.New("unexpected systemctl call")
		}
		s.reloads++
		return []byte("[nginx] reloaded.\n"), s.reloadErr
	}
	if path != "/usr/syno/bin/synowebapi" || !contains(args, "api=SYNO.Core.AppPriv.Rule") || !contains(args, "version=1") {
		return nil, errors.New("unexpected DSM call")
	}
	switch {
	case contains(args, "method=list"):
		if s.listErr {
			return []byte(`{"success":false,"error":{"code":3400}}`), nil
		}
		app := ""
		for _, a := range args {
			if strings.HasPrefix(a, "app_id=") {
				json.Unmarshal([]byte(strings.TrimPrefix(a, "app_id=")), &app)
			}
		}
		rules := []map[string]any{}
		for _, r := range s.rules {
			if strings.HasPrefix(r["app_id"].(string), app) {
				rules = append(rules, r)
			}
		}
		return json.Marshal(map[string]any{"success": true, "data": map[string]any{"rules": rules}})
	case contains(args, "method=delete"):
		s.deleteBudget = commandTimeout(ctx)
		if s.deleteErr {
			return []byte(`{"success":false,"error":{"code":3400}}`), nil
		}
		for _, a := range args {
			if strings.HasPrefix(a, "rules=") {
				s.deleted = append(s.deleted, strings.TrimPrefix(a, "rules="))
			}
		}
		s.rules = nil
		if s.deleteLate != nil {
			return nil, s.deleteLate
		}
		return []byte(`{"success":true}`), nil
	}
	return nil, errors.New("unexpected AppPriv method")
}

func appRule(app, kind, name string) map[string]any {
	return map[string]any{"app_id": app, "entity_type": kind, "entity_name": name, "allow_ip": []string{"0.0.0.0"}, "deny_ip": []string{}}
}

// leftoverFixture lays out DSM's package, app.d and nginx folders; portal
// says whether the package declares the web-config app-portal resource.
func leftoverFixture(t *testing.T, portal bool) leftoverPaths {
	t.Helper()
	root := t.TempDir()
	paths := leftoverPaths{Packages: filepath.Join(root, "packages"), AppPortal: filepath.Join(root, "app.d"), NginxConf: filepath.Join(root, "conf.d")}
	conf := filepath.Join(paths.Packages, "TestApp", "conf")
	for _, dir := range []string{conf, paths.AppPortal, paths.NginxConf} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	resource := `{"systemd-user-unit":{}}`
	if portal {
		resource = `{"systemd-user-unit":{},"web-config":{"app-portal":{"json-relpath":"etc/portal.json","mustache-relpath":"etc/portal.mustache"}}}`
	}
	if err := os.WriteFile(filepath.Join(conf, "resource"), []byte(resource), 0644); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestPurgeDeletesOnlyThisAppsPrivilegeRules(t *testing.T) {
	cmd := &leftoverDSM{rules: []map[string]any{
		appRule("SYNO.SDS.TestApp.Instance", "group", "administrators"),
		appRule("SYNO.SDS.TestApp.Instance", "group", `EXAMPLE\Domain Admins`),
		appRule("SYNO.SDS.TestApp.InstanceX", "user", "fixtureuser1"),
		appRule("SYNO.SDS.TestApp.Instance", "", ""),
	}}
	var logs []string
	purgeLeftovers(context.Background(), &liveDSM{Command: cmd}, "TestApp", leftoverFixture(t, false), func(s string) { logs = append(logs, s) })
	want := `[{"app_id":"SYNO.SDS.TestApp.Instance","entity_name":"administrators","entity_type":"group"},{"app_id":"SYNO.SDS.TestApp.Instance","entity_name":"EXAMPLE\\Domain Admins","entity_type":"group"}]`
	if len(cmd.deleted) != 1 || cmd.deleted[0] != want {
		t.Fatal(cmd.deleted, logs)
	}
	if cmd.reloads != 0 {
		t.Fatal("reloaded nginx without a portal")
	}
}

func TestPurgeWithoutPrivilegeRulesDeletesNothing(t *testing.T) {
	cmd := &leftoverDSM{rules: []map[string]any{appRule("SYNO.SDS.OtherApp.Instance", "user", "fixtureuser1")}}
	purgeLeftovers(context.Background(), &liveDSM{Command: cmd}, "TestApp", leftoverFixture(t, false), func(string) {})
	if len(cmd.deleted) != 0 {
		t.Fatal(cmd.deleted)
	}
}

func TestPrivilegeFailuresAreLoggedAndTheReloadStillRuns(t *testing.T) {
	for name, cmd := range map[string]*leftoverDSM{
		"list":   {listErr: true},
		"delete": {deleteErr: true, rules: []map[string]any{appRule("SYNO.SDS.TestApp.Instance", "user", "fixtureuser1")}},
	} {
		t.Run(name, func(t *testing.T) {
			var logs []string
			purgeLeftovers(context.Background(), &liveDSM{Command: cmd}, "TestApp", leftoverFixture(t, true), func(s string) { logs = append(logs, s) })
			if !strings.Contains(strings.Join(logs, "\n"), "app privilege cleanup") || cmd.reloads != 1 {
				t.Fatal(logs, cmd.reloads)
			}
		})
	}
}

func TestPortalReloadWaitsForDSMToRemoveTheAlias(t *testing.T) {
	for _, file := range []string{"app.d/TestApp.json", "conf.d/alias.syno-app-portal.TestApp.conf", "conf.d/.alias.syno-app-portal.TestApp.conf", "conf.d/dsm.syno-app-portal.TestApp.conf"} {
		t.Run(file, func(t *testing.T) {
			paths := leftoverFixture(t, true)
			left := filepath.Join(filepath.Dir(paths.AppPortal), file)
			os.WriteFile(left, []byte("x"), 0644)
			cmd := &leftoverDSM{}
			var logs []string
			purgeLeftovers(context.Background(), &liveDSM{Command: cmd}, "TestApp", paths, func(s string) { logs = append(logs, s) })
			if cmd.reloads != 0 || !strings.Contains(strings.Join(logs, "\n"), "portal still registered") {
				t.Fatal(cmd.reloads, logs)
			}
		})
	}
}

func TestPortalReloadIgnoresOtherAppsAliases(t *testing.T) {
	paths := leftoverFixture(t, true)
	os.WriteFile(filepath.Join(paths.AppPortal, "TestAppX.json"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(paths.NginxConf, "alias.syno-app-portal.OtherApp.conf"), []byte("x"), 0644)
	cmd := &leftoverDSM{}
	purgeLeftovers(context.Background(), &liveDSM{Command: cmd}, "TestApp", paths, func(string) {})
	if cmd.reloads != 1 {
		t.Fatal(cmd.reloads)
	}
}

func TestPortalReloadFailureIsOnlyLogged(t *testing.T) {
	cmd := &leftoverDSM{reloadErr: errors.New("exit 1")}
	var logs []string
	purgeLeftovers(context.Background(), &liveDSM{Command: cmd}, "TestApp", leftoverFixture(t, true), func(s string) { logs = append(logs, s) })
	if cmd.reloads != 1 || !strings.Contains(strings.Join(logs, "\n"), "nginx reload failed") {
		t.Fatal(cmd.reloads, logs)
	}
}

func TestPortalStateUnknownSkipsTheReload(t *testing.T) {
	paths := leftoverFixture(t, true)
	os.RemoveAll(paths.Packages)
	cmd := &leftoverDSM{}
	var logs []string
	purgeLeftovers(context.Background(), &liveDSM{Command: cmd}, "TestApp", paths, func(s string) { logs = append(logs, s) })
	if cmd.reloads != 0 || !strings.Contains(strings.Join(logs, "\n"), "portal declaration unavailable") {
		t.Fatal(cmd.reloads, logs)
	}
}

// Purge cleans DSM's leftovers even when the task inventory is unavailable.
func TestPurgeWithoutInventoryStillCleansLeftovers(t *testing.T) {
	cmd := &leftoverDSM{rules: []map[string]any{appRule("SYNO.SDS.TestApp.Instance", "user", "fixtureuser1")}}
	state := filepath.Join(t.TempDir(), "state")
	runtime := filepath.Join(state, "runtime")
	os.MkdirAll(runtime, 0700)
	if err := purgeProtectedWith(context.Background(), &liveDSM{Command: cmd}, "TestApp", state, runtime, t.TempDir(), leftoverFixture(t, false), rootPurge{}); err == nil {
		t.Fatal("missing policy falsely reported complete")
	}
	if len(cmd.deleted) != 1 {
		t.Fatal(cmd.deleted)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal("pending purge lost protected state", err)
	}
}

func TestPrivilegeDeleteUsesOperationBudgetWithinRemainingPurge(t *testing.T) {
	cmd := &leftoverDSM{rules: []map[string]any{appRule("SYNO.SDS.TestApp.Instance", "user", "fixtureuser1")}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	purgeLeftovers(withCommandTimeout(ctx, 600*time.Millisecond), &liveDSM{Command: cmd}, "TestApp", leftoverFixture(t, false), func(string) {})
	if cmd.deleteBudget <= 0 || cmd.deleteBudget > 600*time.Millisecond {
		t.Fatal(cmd.deleteBudget)
	}
}

// A delete error is not the outcome: the rules listed afterwards decide it,
// and the journal says why the delete call failed.
func TestPrivilegeDeleteErrorIsSettledByListingAgain(t *testing.T) {
	for name, tc := range map[string]struct {
		cmd  *leftoverDSM
		want string
	}{
		"late but deleted":           {&leftoverDSM{deleteLate: context.DeadlineExceeded}, "app privilege cleanup: app=SYNO.SDS.TestApp.Instance rules=1 delete_error=timeout"},
		"refused, rules left":        {&leftoverDSM{deleteErr: true}, "app privilege cleanup failed: app=SYNO.SDS.TestApp.Instance rules_left=1 delete_error=dsm-3400"},
		"command failed but deleted": {&leftoverDSM{deleteLate: errors.New("DSM command")}, "app privilege cleanup: app=SYNO.SDS.TestApp.Instance rules=1 delete_error=command"},
	} {
		t.Run(name, func(t *testing.T) {
			tc.cmd.rules = []map[string]any{appRule("SYNO.SDS.TestApp.Instance", "user", "fixtureuser1")}
			var logs []string
			purgeLeftovers(context.Background(), &liveDSM{Command: tc.cmd}, "TestApp", leftoverFixture(t, false), func(s string) { logs = append(logs, s) })
			if strings.Join(logs, "\n") != tc.want {
				t.Fatal(logs)
			}
		})
	}
}

// purgeFixture is a protected state folder whose runtime holds policy.
func purgeFixture(t *testing.T, policy Policy) (state, runtime string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state = filepath.Join(root, "state")
	runtime = filepath.Join(state, "runtime")
	if err := os.MkdirAll(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(policy)
	if err := os.WriteFile(filepath.Join(runtime, "policy.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return state, runtime
}

// Uninstalling directly from Package Center leaves no Application Portal
// entry the executor registered (the regression test found DSM 7.1 keeps all of it).
func TestPurgeRemovesTheExecutorsPortalEntry(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	if err := p.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.register("alias.syno-app-portal.Demo.conf", false)
	state, runtime := purgeFixture(t, portalPolicy("Demo"))
	var logs []string
	purgePortalEntry(context.Background(), &liveDSM{Command: d}, "Demo", state, runtime, p.Paths, func(s string) { logs = append(logs, s) })
	if got := p.Entry(); got != PortalUnregistered {
		t.Fatal(got, logs)
	}
	if strings.Join(logs, "\n") != "portal entry cleanup: package=Demo" {
		t.Fatal(logs)
	}
}

func TestPurgeLogsAPortalEntryItCouldNotRemove(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	if err := p.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.fail["nginx -t"] = true
	state, runtime := purgeFixture(t, portalPolicy("Demo"))
	var logs []string
	purgePortalEntry(context.Background(), &liveDSM{Command: d}, "Demo", state, runtime, p.Paths, func(s string) { logs = append(logs, s) })
	if strings.Join(logs, "\n") != "portal entry cleanup failed: package=Demo" {
		t.Fatal(logs)
	}
}

func TestPurgeWithoutAPortalEntryTouchesNoPortalFile(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	// A web-config resource's files are DSM's to remove, even under this name.
	os.WriteFile(filepath.Join(p.Paths.AppD, "Demo.json"), []byte(`{}`), 0644)
	policy := portalPolicy("Demo")
	policy.Portal, policy.Operations = nil, []string{"status"}
	state, runtime := purgeFixture(t, policy)
	var logs []string
	purgePortalEntry(context.Background(), &liveDSM{Command: d}, "Demo", state, runtime, p.Paths, func(s string) { logs = append(logs, s) })
	if len(d.calls) != 0 || len(logs) != 0 {
		t.Fatal(d.calls, logs)
	}
	if _, err := os.Stat(filepath.Join(p.Paths.AppD, "Demo.json")); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeWithAnUnreadablePolicyLeavesThePortalAlone(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	state, runtime := purgeFixture(t, portalPolicy("Demo"))
	os.WriteFile(filepath.Join(runtime, "policy.json"), []byte(`{"v":1`), 0600)
	var logs []string
	purgePortalEntry(context.Background(), &liveDSM{Command: d}, "Demo", state, runtime, p.Paths, func(s string) { logs = append(logs, s) })
	if len(d.calls) != 0 || strings.Join(logs, "\n") != "portal entry cleanup skipped: policy unavailable: package=Demo" {
		t.Fatal(d.calls, logs)
	}
}

func TestPurgeProtectedRemovesThePortalEntryBeforeTheState(t *testing.T) {
	d, p := portalFixture(t, "Demo")
	if err := p.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, runtime := purgeFixture(t, portalPolicy("Demo"))
	paths := leftoverFixture(t, false)
	paths.Entry = p.Paths
	paths.Packages = filepath.Join(t.TempDir(), "packages")
	os.MkdirAll(filepath.Join(paths.Packages, "Demo", "conf"), 0755)
	os.WriteFile(filepath.Join(paths.Packages, "Demo", "conf", "resource"), []byte(`{}`), 0644)
	if err := purgeProtectedWith(context.Background(), &liveDSM{Command: portalPurgeDSM{d}}, "Demo", state, runtime, t.TempDir(), paths, rootPurge{}); err != nil {
		t.Fatal(err)
	}
	if got := p.Entry(); got != PortalUnregistered {
		t.Fatal(got, d.calls)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("state kept", err)
	}
}

type portalPurgeDSM struct{ *portalDSM }

func (d portalPurgeDSM) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	if path == "/usr/syno/bin/synowebapi" && contains(args, "api=SYNO.Core.AppPriv.Rule") {
		return []byte(`{"success":true,"data":{"rules":[]}}`), nil
	}
	return d.portalDSM.Exec(ctx, path, args)
}
