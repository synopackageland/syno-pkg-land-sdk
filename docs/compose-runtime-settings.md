# Native installation settings

Use SDK and CLI 0.27.1 for native installation settings and runtime setting bindings.

Schema 2 Apps declare installation configuration without App hooks or SPK edits:

```yaml
install:
  settings:
    public_origin:
      type: https-origin
      label: Public HTTPS origin (include port 29443)
      required: true
identity_gateway:
  listen: {https: 29443}
# For Compose services:
# storage_init.runtime.environment:
#   DOMAIN: {setting: public_origin}
```

The CLI renders the fields through the same static/localized/dynamic install
wizard as `install.sharedFolder`, preserving consent, existing-data notices and
App hooks. Keys are lowercase ASCII letters/digits/underscores, start with a
letter and have at most 32 characters. There are at most 32 settings. Labels are
plain text (1–160 characters); required is an explicit boolean. No defaults,
request headers, secret values or arbitrary file paths are accepted.

The SDK's package-account `install/control` owns validation and persistence:

- `prepare` in preinst reads `pkgwizard_setting_<key>` from the installer
  environment. Blank reuses retained state; missing required values fail closed.
  An explicitly invalid nonblank value never falls back to retained state.
- Only a literal root `https://hostname[:port]` origin is admitted. Credentials,
  path (including trailing slash), query, fragment, controls, invalid DNS labels,
  IPv6 literals and IDNs are rejected. If the App has a native HTTPS gateway,
  the explicit origin port must equal that listener. Without it, omitted port
  means standard HTTPS; any explicit port must be canonical decimal 1–65535.
- Values are written by exclusive temporary file, fsync and atomic rename to
  `target/install/values/<key>.value`, mode 0600, within a private directory.
- `remember` in postinst/postupgrade revalidates and saves private values under
  DSM-owned pkgvar's `install-settings/`. Preinst never creates the pkgvar root.
  Upgrade reads the DSM pkgvar environment or the selected volume's exact
  `@appdata/<package>` when the convenience package symlink is absent.
- Keep-data uninstall/reinstall reuses this state. Explicit delete-data removes
  only the bounded SDK settings directory; upgrade never purges it. Container
  Manager named-volume retention is an independent contract.

Compose runtime `{setting: key}` must reference a declared install setting. The
SDK binds just that file read-only at `/synopkgland/settings/<key>.value` and
loads it as a literal environment value before upstream starts. Missing/private
file admission errors stop preparation/startup. RuntimeSettingsVersion is 2.
Non-Compose Apps can read the same private target files; no backend-specific
App installation script is needed.

## Security boundaries

| Boundary | Handling | Remaining scope |
| --- | --- | --- |
| Issuer/admission | Settings never change identity gateway policy or issue credentials | DSM App privilege checks remain mandatory |
| Validation | SDK validates origin and fixed native gateway port on input and persistence | DNS resolution, certificate trust and actual endpoint reachability need deployment acceptance |
| Files | Package/current UID, private regular single-link files, bounded reads, no-follow opens, rooted paths; atomic 0600 writes | Package UID and host administrator are trusted; a compromised App can change its own configuration |
| Lifecycle | SDK incoming/retained paths; no root, chown, App fallback or DSM system writes | DSM must create and own pkgvar before postinst |
| Execution | Literal values only; no shell evaluation or interpolation | Upstream decides what its environment variables mean |
| Inspection | CLI compares declarations, private inventory, wizard and SDK hook calls; values may not ship | ELF format inspection does not attest SDK source provenance |
| HTTPS | Require administrator's explicit origin; do not infer external URL from headers or bare hostnames | Reverse-proxy aliases, subpaths, IPv6 and IDNs need a separate supported contract; no automatic DSM certificate/proxy changes |

Blank wizard fields are allowed so reinstall can reuse private state. The SDK
still enforces `required` for a fresh install. The UI never displays retained
values or copies them into the SPK/check/build/inspect reports.
