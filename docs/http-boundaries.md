# HTTP boundaries

Use SDK/App 0.36.0 with CLI 0.38.0. JSON operations use `api.Handle`,
`HandleRequest`, `HandleResponse` or query-selected `HandleStream`.

## Fixed browser routes

Register an HTTP prefix as part of the same `api.NewServerWithPermissions`
route inventory:

```go
route := api.HandleHTTP("/proxy/", "app.proxy", proxyHandler)
```

Declare the permission key in the existing table:

```json
{"schema":1,"default":"administrators","methods":{"app.proxy":"administrators"}}
```

The second argument is an App method key, not an inline permission or user
name. `authenticated` and `administrators` are the only raw HTTP permissions.
Public or undeclared raw routes fail startup. Every request passes origin
validation, the App entry gate, identity verification and the route permission
before the handler runs. `api.HTTPCaller(r.Context())` returns the verified
caller. Route admission does not grant root or authorize an upstream target;
the App must check target approval and strip credentials before proxying.

Prefixes must be literal paths such as `/proxy/`, end in `/`, use only
ASCII letters, digits, `_` and `-` in segments, and have no overlap. The root,
SDK `/_sso/`, `/_spl/`, `/api/`, `/api.cgi/`, `/rpc/`, `/webman/` and `/cgi/`
paths are reserved.
`/proxy/` matches `/proxy/item` but not `/proxySibling/` or `/proxy`.
Encoded, backslash, dot-segment and repeated-slash request paths are refused
before dispatch. The handler receives the original URL, query and body;
`?method=` does not select another method on a raw route.
CGI matches the path after its trusted `SCRIPT_NAME`: a `/proxy/` registration
can receive `/webman/3rdparty/SampleApp/cgi/api.cgi/proxy/item` where the web
server supports PATH_INFO. The handler retains that original URL; the App
chooses how to rewrite it for an upstream. SDK identity, description and
deadline middleware preserve this raw routing without parsing its body.

Raw method keys cannot be invoked through JSON, batch or query-selected
transfers. CLI builds hash the sorted `{prefix,method}` inventory and bind
`api.expectedHTTPRoutesDigest` alongside entry and permission digests.
Startup and route description refuse a changed or removed inventory.
Keep one App registry per binary; internal SDK ceremony servers remain separate.

Handlers retain `http.Flusher`, `http.Hijacker` and `http.ResponseController`
access to the underlying transport. HTTP/1 upgrade support is exercised with a
real HTTP server; a CGI ResponseWriter cannot hijack a connection. Registering
a raw route does not turn CGI into a WebSocket listener or install a DSM
ingress. Use a deployment transport that already supports upgrades. No DSM
WebSocket deployment acceptance is claimed. Streams retain the server request
deadline; account revocation after an upgrade is the App's responsibility.

## Integration callbacks

Some callers cannot hold a DSM session: Chat button and slash-command
callbacks, Drive webhooks and third-party webhooks. Register each one with a
fixed method name:

```go
route := api.HandleIntegration("app.chat.callback", callbackHandler)
```

```json
{"schema":1,"default":"administrators","methods":{"app.chat.callback":"integration"}}
```

```yaml
integrations:
  - app.chat.callback
```

The request selects the route by exactly one `?method=app.chat.callback`
query value, so a callback URL such as `.../api.cgi?method=app.chat.callback`
keeps working. Repeated or unparsable `method` values are refused. Prefix
routes are matched first; an integration selector never matches a path that
belongs to a `HandleHTTP` prefix.

`"integration"` is valid only with `HandleIntegration`, and
`HandleIntegration` accepts no other permission. For these methods only, the
SDK skips the DSM session and the App entry gate and hands the original
request to the handler with no verified caller. Everything else is unchanged:
JSON, batch and `?method=` transfer calls cannot reach an integration method,
and every other method still requires a session. A request carrying
`Origin`, `Referer` or `Sec-Fetch-Site` must pass `CheckOrigin`; service
callbacks normally carry none.

The handler is the authentication boundary. Before acting it must verify the
caller with a secret the App holds: a bot token, an HMAC signature, a webhook
key or a signed, expiring locator. Bound its body, method and content type.
The SDK cannot check these for it.

To act for a DSM account, call `api.ActAs` after that verification:

```go
caller, err := api.ActAs(ctx, r, username)
```

`ActAs` works only inside an integration handler and only when the server's
authenticator implements `api.PrincipalAdmitter`, as `dsm.EntryGate` does. The
gate refuses an account that does not exist, is disabled or has any expiry
date (`api.ErrAccountDisabled`), or that DSM Application Privileges do not
allow from 127.0.0.1 (`api.ErrAppPrivilegeDenied`). An `admin-only` App
admits no account this way. The returned caller is never an administrator,
even for an administrator account. Unknown results refuse. The account check
runs the native account bridge as the package account, which has not yet been
verified on a device; if DSM refuses it there, `ActAs` refuses every account.

CLI builds hash integration selectors into the HTTP route inventory as
`{"prefix":"?method=<method>","method":"<method>"}`, so startup refuses a
binary whose callbacks differ from the audited list. Discovery reports them
with kind and permission `integration`. Handlers reachable from a
`HandleIntegration` handler argument may construct `http.HandlerFunc`, as for
`HandleHTTP`.

## In-process administrator calls

A package-account command that runs outside CGI and already authenticated the
person who asked, such as a helper daemon's `api.cgi <subcommand>`, may need
one App route's result for that administrator. Build the App's normal server
with `dsm.NewEntryGate` and call it in process:

```go
out, err := server.InvokeAsAdministrator(ctx, user, "app.support.propose", params)
```

The SDK, not the App, decides who runs it. `dsm.EntryGate.AdmitAdministrator`
asks DSM's native role provider whether `user` is a current administrator and
requires the account to be enabled and unexpired; the route then runs with an
administrator `Caller` under the same permission and entry checks as a request.

It refuses:

- under CGI (`GATEWAY_INTERFACE` set) and inside any request the server is
  dispatching, so no request can choose the account it runs as;
- integration, raw HTTP, stream, SDK and unknown methods;
- params that are not one JSON value within the server's request limit.

Never call `ServeHTTP` on a server in process to obtain a `Caller`; the CLI
route audit rejects it. The CLI audit requires a literal method declared
`administrators` in permissions.json for each `InvokeAsAdministrator` call.

## Private worker IPC

```go
err := ipc.ServeUnix(ctx,
    "/var/packages/SampleApp/var/run/control.sock", privateHandler)

transport := &http.Transport{
    DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
        return ipc.DialUnix(ctx,
            "/var/packages/SampleApp/var/run/control.sock")
    },
}
client := &http.Client{Transport: transport}
```

The CLI binds `ipc.packageID` in each Go binary it builds. Direct development
builds must bind it with `-ldflags '-X
github.com/synopackageland/syno-pkg-land-sdk/ipc.packageID=SampleApp'`.
The helper requires Linux and the non-root package account. It creates or
opens an owned mode-0700 `var/run` without following a `run` symlink, pins that
directory, binds a Unix socket, and sets mode 0600 before serving HTTP. It
does not change process-wide umask, return a listener, or accept TCP addresses,
other package paths, nested socket directories or caller-selected modes.
An existing path is refused, including stale sockets; inspect and remove a
stale socket explicitly while the worker is stopped. Cancellation closes
connections and removes only the socket the helper created.

For independent data-plane sockets, use `ipc.StartUnix(ctx, path, handler)`:

```go
site, err := ipc.StartUnix(ctx,
    "/var/packages/SampleApp/var/run/site-one.sock", proxy)
if err != nil {
    return err
}
defer site.Close()
// On stop or authority revocation:
err = site.Shutdown(stopContext)
```

Each path names a different direct child of the same run directory.
`StartUnix` returns after binding and securing the socket, or returns a startup
error synchronously. Its handle exposes `Close`, `Shutdown` and `Wait`, never
a listener. `ServeUnix` is the blocking convenience form. Both forms track
accepted connections through their entire lifetime, including HTTP hijacks.
Cancellation, Close and Shutdown close active streams and hijacked connections
and remove only the owned socket. Shutdown is a stop/revoke operation, not a
promise to drain active streams. Stopping one server leaves the others running.
Handlers must propagate cancellation to upstream work and enforce their own
target approval, pinning and authentication. This data plane can serve root
paths, WebSocket and streams without routing through CGI or granting browser
ingress.

The matching dial helper checks socket ownership, type and exact mode 0600.
This is local IPC within one package account, not a user-identity boundary or
a DSM nginx ingress. Other processes running as that account are trusted.

## Ports for supervised children

```go
reservation, err := ipc.ReserveLoopback(ctx, 0)
if err != nil {
    return err
}
defer reservation.Release()
address := reservation.Address() // always 127.0.0.1:<port>
// Configure the official child binary to bind address.
if err := reservation.Release(); err != nil {
    return err
}
// Start the child and verify readiness; retry allocation on bind failure.
```

Zero selects an ephemeral TCP4 loopback port; a nonzero `uint16` requests a
specific port. `Port` returns its number. The reservation holds the bind until
Release and has no Accept, listener or serving API.
`ipc.ProbeLoopback(ctx, port)` checks a specific nonzero port and releases it
immediately. Neither API proves future availability. An official binary that
cannot inherit the descriptor has a release-to-bind race; supervise it, verify
readiness and retry on conflict. These APIs neither start nor elevate a child
and do not reserve UDP or wildcard/IPv6 addresses.

## Optional executor peer protocol

An optional helper is a separate, explicitly approved executor payload, with
protected installation, digest approval and stop/revoke lifecycle. Importing
a client never approves, installs or starts that payload. Ordinary CGI/worker
builds bind `ipc.executorTier=none` even when using SDK executor capabilities.
The SDK provides no setuid, sudo or root process launch.

The protected helper build binds both `ipc.packageID=SampleApp` and
`ipc.executorTier=executor` with `-X` linker flags. `ipc.StartPeerUnix` also
requires effective UID zero. Bind only the separately approved helper this
way; never a CGI, package worker or package-writable executable.

```go
peerServer, err := ipc.StartPeerUnix(ctx,
    "/var/run/synopkgland/SampleApp/helper.sock",
    ipc.PeerPolicy{UID: packageUID, GID: packageGID},
    func(ctx context.Context, conn net.Conn, peer ipc.PeerCredentials) {
        // Decode a bounded frame and verify protocol/current approval.
        // Own conn until explicitly closing it, server Close or process exit.
        // Returning from this callback leaves conn open.
    })
if err != nil {
    return err
}
defer peerServer.Close()
```

`StartPeerUnix` returns only after the socket is bound and secured; startup
errors are synchronous. It exposes no listener. The handler owns the verified
connection, including after callback return, and must close it when finished.
`ServePeerUnix` is the blocking convenience form: it closes each connection
when its callback returns and closes all connections when serving stops.

Linux pins root-owned, non-group/world-writable directories beneath the
platform `/var/run` anchor and refuses child symlinks. Each socket is root-owned
0660 with the selected GID; directory ownership stays with root. Existing paths
are refused. SO_PEERCRED admits exactly `PeerPolicy.UID` before the callback;
UID zero explicitly selects a root-to-root channel. There are eight admission
slots. A slot stays occupied until both its callback returns and its connection
closes, so retaining a connection or closing one early cannot bypass the limit.
Each connection starts with a five-second deadline. The handler must bound
frame sizes, validate methods and digest/grant checks, honor cancellation and
set explicit deadlines for longer exchanges.

`peerServer.StopAccepting()` closes the listener, removes only its owned
socket and returns after the accept loop exits. New dials fail; accepted,
verified connections and handler contexts remain usable. It can be called
from a callback and does not wait for callbacks. `Wait()` waits for that same
admission stop and socket cleanup, not for retained peers or handler completion.
Explicit StopAccepting/Close return nil on success; context cancellation and
unexpected serving or cleanup failures are reported by Wait.

A supervisor's ordered shutdown is:

1. Stop and reap its child on the OS thread that owns the child lifecycle.
2. Call StopAccepting and check its result.
3. Write the complete final ACK on the existing verified connection.
4. Retain that connection until process exit so the client receives ACK then EOF.

Keep the server context alive through the ACK. `Close()` or cancellation of
the original context cancels handler contexts and closes every accepted peer,
even after StopAccepting; use these for abort/revoke, not before the final ACK.
Close waits for admission stop and socket cleanup; handlers must stop their
own work. After StopAccepting, call Close when retained peers are no longer
needed, or exit the helper process. Connection deadlines still apply to I/O;
they do not themselves close a retained descriptor or free its slot.

`ipc.DialPeerUnix` checks root ownership, exact mode, protected ancestors and
server UID zero. Ordinary package processes may use the client without an
executor binding. Neither this transport nor a successful audit proves the
helper's privileged operations safe.

## Source audit

The CLI resolves `go list` import graphs for the configured backend main and
the declared background worker, separately for every packaged Linux
architecture with CGO disabled. Unimported `cmd/dev`, tools, tests and files
excluded by target build tags are not shipped and are not audited. A dev
package imported by a shipped root becomes audited. Local replacement modules
are included. Published dependencies and prebuilt/custom-hook executables
remain trusted packaging inputs; the audit is not a whole-program sandbox.

Only handler declarations reachable from a direct `HandleHTTP`,
`ipc.ServeUnix` or `ipc.StartUnix` handler argument may construct local HTTP muxes/handler
functions or implement `ServeHTTP`. This includes local factories and types
in imported local packages. Unrelated handlers remain rejected. Dynamic
prefixes, constructor aliases, undeclared permissions, source symlinks,
global default-mux handlers and direct HTTP/TCP/TLS/other listeners remain
rejected even when they reuse an approved IPC handler. Build repeats the audit
after UI hooks. Runtime inventory checks remain authoritative for SDK routes.
Raw HTTP registrations may appear in discovery, but cannot back ctl RPC commands.

Listener types such as `net.Listener` and `net.ListenConfig` create no entry.
Real listener constructors, their aliases and direct serving calls remain
rejected; declared `ListenConfig` and `http.Server` serving methods are also
checked. Direct SDK port and peer-IPC calls are supported boundaries; SDK
constructor aliases are refused.

Custom prepare hooks must audit every shipped Go entry independently:
`synopkgland audit-entry ./cmd/helper --dir backend --json`. Select the same
`--arch amd64,arm64,arm` targets as the build. Supply `--permissions` relative
to the module directory when an entry registers App methods. The command
resolves only that entry's actual import graph without running init/main.
Regular backend/worker build audits use the same rule. Importing a package
includes all its target-selected files, including unused server functions;
there is no function-level dead-code exemption. Split client/server
responsibilities into real packages or migrate shared server code to SDK
boundaries. Do not hide files, rename imports or exempt whole packages.
Custom hooks remain responsible for their protected executor build binding
and approval.
