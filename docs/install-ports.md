# Installer-selected ports

The CLI's `install.ports: true` binds the manifest's gateway listeners, local
OIDC issuer port and second origin port to the DSM installation wizard. `installsettings.PreparePorts`
checks the selected values, consults DSM's native service-conflict tool and
materializes only generated payload files. It never runs as root, changes DSM
system files, opens a container socket or evaluates shell input.

`RememberPorts` stores private decimal values under the DSM-owned package var's
`install-ports` directory. Upgrades read those values and ignore attempted port
changes.

## Change ports after installation

`ChangePorts(payload, pkg, pkgvar, map[string]int{key: port})` changes installed
ports from an App's own settings, run as the package account. Only keys declared
in `install/ports.json` change. It applies the same format, range, reserved,
duplicate and conflict checks as installation (a warn-policy conflict is returned
as a warning), then rewrites the generated payload files, `install/<package>.sc`,
`install/ports.env`, the selected values and the retained `install-ports` values,
so upgrades keep the change. A refused change writes nothing. DSM applies the new
nginx and port-config resources only when the package starts, and the gateway
reads the new value when it starts: restart the package afterwards, for example
with [`selfrestart`](self-package-restart.md). Reinstalling with new wizard values
also works; package data is retained unless its delete-data option is selected.

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
