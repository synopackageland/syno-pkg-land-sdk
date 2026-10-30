# Installer-selected ports

The CLI's `install.ports: true` binds the manifest's gateway listeners and local
OIDC issuer port to the DSM installation wizard. `installsettings.PreparePorts`
checks the selected values, consults DSM's native service-conflict tool and
materializes only generated payload files. It never runs as root, changes DSM
system files, opens a container socket or evaluates shell input.

`RememberPorts` stores private decimal values under the DSM-owned package var's
`install-ports` directory. Upgrades read those values and ignore attempted port
changes. Reinstall is the supported way to choose different ports; package data
is retained unless its explicit delete-data option is selected.

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

Supported keys are `gateway_http`, `gateway_https` and `oidc_issuer` when declared.
Container-internal and fixed discovery ports retain their upstream contract.
Port checks cannot reserve a listener atomically; DSM resource acquisition and
binding are the final checks. DSM device acceptance remains separate from unit
and generated-package tests.
