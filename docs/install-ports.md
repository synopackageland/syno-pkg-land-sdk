# Installer-selected ports

The CLI's `install.ports: true` binds the manifest's gateway listeners, local
OIDC issuer port and second origin port to the DSM installation wizard. `installsettings.PreparePorts`
checks the selected values, consults DSM's native service-conflict tool and
materializes only generated payload files. It never runs as root, changes DSM
system files, opens a container socket or evaluates shell input.

`RememberPorts` stores private decimal values under the DSM-owned package var's
`install-ports` directory. Installation over retained package data and upgrades
read those values before wizard defaults. A fresh installation uses wizard
values, then manifest defaults. An upgrade ignores wizard port inputs.

## Change ports after installation

`ChangePorts(payload, pkg, pkgvar, map[string]int{key: port})` saves ports for the
next installation or upgrade from an App's own settings, run as the package
account. Only keys declared
in `install/ports.json` change. It applies the same format, range, reserved,
duplicate and conflict checks as installation (a warn-policy conflict is returned
as a warning), then writes only the retained `install-ports` values. Subsequent
edits merge with that saved selection; selecting the effective port again
cancels its pending change. Invalid or conflicting input writes nothing.

The installed daemon configuration, nginx files, gateway configuration,
`install/<package>.sc`, `install/ports.env` and selected runtime values stay
unchanged. Stopping and starting the package continues to use the effective
ports and preserves the daemon inventory digest. No restart is requested.

### Apply saved ports in Package Center

1. Open **Package Center > Manual Install** and select the package's SPK file.
   Use the same SPK to reinstall, or a newer SPK to upgrade. The installer accepts
   an equal version as well as a newer version; older versions are refused.
2. Complete the installation wizard. When package data is retained, the saved
   port selection takes precedence over wizard defaults. The SDK's `prepare`
   hook renders the incoming daemon configuration, nginx templates and service
   definitions before DSM acquires their resources. The `remember` hook in
   `postinst` or `postupgrade` retains the installed selection.
   A retained `https-origin` setting keeps its host and adopts the selected
   gateway HTTPS port in the incoming payload. Explicit wizard settings are
   validated as entered; `external-origin` settings keep their independent port.
3. Reopen the App and read its port status. Installation can stop the App and
   close its DSM window. Check listener health after installation completes.

[Synology's package guide](https://help.synology.com/developer-guide/getting_started/first_package.html)
documents the native Manual Install operation. Equal-version acceptance is
supported by the package installer's version validation; it is not a guarantee
that every package passes all other installation checks. If DSM rejects the
same version, leave the saved ports pending and wait for the next upgrade.
Do not uninstall the package to force a port change or select a delete-data
option. A restart alone cannot regenerate DSM's installed nginx resources.

Show `installsettings.PortsPendingMessage` while changes are pending (Apps
translate this English hint):

> Port changes are saved. In Package Center, use Manual Install to reinstall this
> package or install an upgrade. If DSM rejects the same version, wait for the
> next upgrade. Restarting does not apply port changes.

### Read configured and effective ports

`ReadPortStatus(payload, pkg, pkgvar)` returns a map keyed by each declared port:

```json
{"gateway_origin":{"configured":28600,"effective":28443,"pending":true}}
```

`configured` is the saved selection for installation; `effective` is the port
materialized in this installed payload; `pending` compares the two. Missing
retained values fall back to the installed selection, while invalid private
state returns an error. The status becomes non-pending when the replacement
payload contains the saved selection. Reading the old payload during an upgrade
still describes that old installation.

Effective configuration alone cannot prove a socket is listening: the package
may be stopped or unhealthy. An App must combine it with its own listener or
daemon health before labeling it "listening"; a TCP connection to a port alone
does not establish which App owns it. Show the configured and effective values
separately while a change is pending.

## Generated files and checks

`PortValues(payloadRoot)` reads the package-owned selected values. Gateway
configuration loading uses them before validation and binding. Generated daemon
configuration also supplies `SYNOPKGLAND_PORT_*` environment variables. Compose
runtime environment entries such as `{port: gateway_https}` bind the selected
value read-only and expose it to the upstream process as a literal value; runtime
file templates can then use the variable in the app's own configuration.

The CLI generates one `<package>.sc` file and registers it with DSM's native
port-config resource. nginx configuration, DSM launcher code, daemon public-port
inventory and HTTPS-origin validation are materialized from the same selection.
Declared defaults remain build/inspection metadata and are never substituted for
a missing selected runtime value. A failed conflict check aborts installation
before those generated files are rewritten.

Checks invoke `/usr/syno/bin/servicetool` directly, including with DSM's restricted
package-account PATH. A confirmed conflict remains `INSTALL_PORT_INVALID`; the
exact service ID registered for this package and this port key is allowed.
A port declared with `"conflict": "warn"` keeps a confirmed conflict: `PreparePorts`
writes `INSTALL_PORT_CONFLICT_WARNING` to stderr (DSM's installation log) and
`ChangePorts` returns it. Format, range, reserved and duplicate values are always
refused, and a failed check (`INSTALL_PORT_CHECK_FAILED`) still fails. A missing/unexecutable tool, nonzero exit, oversized or malformed
response instead returns `INSTALL_PORT_CHECK_FAILED`. Retry installation and,
if it persists, have the administrator check the DSM tool and service registry.
Changing the selected port does not repair a failed registry check.

`PortCheckError` retains the exit code (`-1` if execution never started or was
interrupted), bounded stderr summary, elapsed duration and underlying error.
`errors.Is(err, ErrPortCheck)` distinguishes these failures from `ErrPort`, even
after the installer appends the affected setting key. Output is captured with a
fixed memory limit and diagnostic control characters are escaped. The check adds
no elapsed-time timeout; a loaded NAS may keep working until the caller explicitly
cancels or its own context deadline expires.

Supported keys are `gateway_http`, `gateway_https`, `oidc_issuer` and
`gateway_origin` when declared. The CLI declares `gateway_origin` with
`"conflict": "warn"`; the others refuse a confirmed conflict. A port that is
really in use still makes the App fail to start.
Container-internal and fixed discovery ports retain their upstream contract.
Port checks cannot reserve a listener atomically; DSM resource acquisition and
binding are the final checks. DSM device acceptance remains separate from unit
and generated-package tests.
