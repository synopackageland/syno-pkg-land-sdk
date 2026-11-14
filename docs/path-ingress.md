# Identity gateway on a DSM path

`idgateway.PathIngressVersion = 2` identifies support for both the default
`/synopkgland/<package>/` and an explicit custom base such as `/my-app/` or
`/my-app/ui/`. Custom segments use lowercase letters, digits and internal
hyphens, with leading and trailing slashes, at most 64 characters per segment
and 255 total. DSM/native prefixes and other packages' namespaces are reserved.
The origin root `/` is unsupported; Apps requiring `/` cannot use ingress.
The CLI generates `native_ingress: true` and
`ingress_path` together. The gateway opens only its private mutually
authenticated Unix socket; the OIDC issuer, when configured, remains on loopback.
The CLI registers the location through DSM's resource worker. Older
`native_ingress` configurations without `ingress_path` retain dedicated-port
behavior.

The nginx location preserves the full request URI. Upstreams must support their
configured base path. Gateway declarations keep paths relative to the package:
policy prefixes, redirects, OIDC client callback paths, browser helpers and the
launcher are scoped once. Sessions and upstream cookies stay under the package
path. DSM cookies and other gateway cookies are stripped before proxying.

## Unix socket upstream

`idgateway.UnixUpstreamVersion = 1` lets the gateway `"upstream"` be
`"unix:/var/packages/<Package>/var/run/<name>.sock"` instead of an
`http://127.0.0.1:<port>` address. The socket is the App's own private SDK IPC
socket (`ipc.StartUnix`/`ipc.ServeUnix`), so the App needs no TCP port or
listener: nginx, the mTLS Unix socket and the gateway reach it under the DSM
origin. `<Package>` must equal the gateway's `package` exactly and `<name>.sock`
must be a direct child of `var/run` (clean absolute path, `.sock` suffix, name at
most 64 characters); anything else, and combining it with `compose_ingress`, is
refused when the configuration loads. The gateway dials through
`ipc.DialPackageUnix`, which checks the directory and socket ownership and
modes like `ipc.DialUnix` but takes the package from the configuration, because
the CLI does not bind `ipc.packageID` into the gateway binary.

Admission, `upstream_auth_paths`, the `Host` header, `X-Synopkgland-*` handling
and path scoping do not change; `/_spl/ping` and the readiness check never touch
the upstream. The gateway sets no deadline on an upgraded connection: it ends only when
the browser or the App closes it, the DSM session is revoked (protected paths), or the
gateway stops. `response_header_timeout_seconds` (default none) only bounds
waiting for response headers, including the `101`. The gateway's own server limits
(30 s to read request headers, 2 minutes idle between requests) do not apply
after the upgrade; nginx's `proxy_read_timeout 3600s` (the CLI default) does, so a quiet tunnel needs
traffic or pings within that interval.

Login authorization permits only a return URL on the CGI's own DSM origin and
this package's exact callback path. Cold-login resume uses a transaction-bound
path token rather than choosing an App port. The nginx hop supplies the original
scheme and peer address over mutual TLS; client forwarding headers never grant
identity. Legacy dedicated-port external-origin routing is not used in this mode.

The CLI's nginx location overwrites `X-Synopkgland-Scheme` with `$scheme`.
Only the mutually authenticated private ingress handler accepts this header;
direct listeners and the authorize CGI use their own TLS state and ignore
forwarded scheme headers. The internal connection stays TLS-authenticated even
when the browser uses HTTP. Cookies, origins, return URLs and upstream forwarding
all use that external scheme. HTTP remains available with an encryption warning;
HTTPS session and binding cookies remain Secure. HTTP ingress uses separate
cookie names, so an earlier HTTPS cookie cannot prevent the browser from storing
the HTTP sign-in cookies. This also keeps HTTP sessions out of HTTPS sign-in.

An expired transaction, missing/mismatched browser binding or invalid assertion
starts a fresh sign-in once on browser navigation. Its transaction carries the
retry guard even if cookies are dropped or the previous transaction is gone.
A repeated failure shows a localized explanation and an explicit sign-in link.
Recovery never redeems the failed assertion or resumes another browser's target.

The CLI owns the public manifest and capability check; see its
[ingress guide](https://github.com/synopackageland/syno-pkg-land-cli/blob/main/docs/path-ingress.md).
Do not construct an nginx rule or edit installed gateway JSON in an application.
An upstream without subpath support cannot be made prefix-aware merely by
changing its gateway configuration.

Before installation or upgrade, the CLI invokes the bundled gateway executable's
read-only `ingress-check` command. `CheckIngressOwnership` checks DSM native
entries, saved aliases, stopped SDK declarations and active nginx locations.
An overlap or unreadable/unknown inventory fails closed with a readable install
error. DSM's worker owns registration, upgrade replacement and uninstall cleanup;
the checker writes no registry and never removes another owner's configuration.
The stable menu CGI launches the selected base. Authentication, App privileges,
relative callback/API/share policy and WebSocket admission are unchanged.
