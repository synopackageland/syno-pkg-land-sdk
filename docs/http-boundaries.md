# HTTP boundaries

Use SDK 0.33.0 with CLI 0.35.0. JSON operations still use `api.Handle`,
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

The matching dial helper checks socket ownership, type and exact mode 0600.
This is local IPC within one package account, not a user-identity boundary or
a DSM nginx ingress. Other processes running as that account are trusted.

## Source audit

The CLI resolves `go list` import graphs for the configured backend main and
the declared background worker, separately for every packaged Linux
architecture with CGO disabled. Unimported `cmd/dev`, tools, tests and files
excluded by target build tags are not shipped and are not audited. A dev
package imported by a shipped root becomes audited. Local replacement modules
are included. Published dependencies and prebuilt/custom-hook executables
remain trusted packaging inputs; the audit is not a whole-program sandbox.

Only handler declarations reachable from a direct `HandleHTTP` or
`ipc.ServeUnix` handler argument may construct local HTTP muxes/handler
functions or implement `ServeHTTP`. This includes local factories and types
in imported local packages. Unrelated handlers remain rejected. Dynamic
prefixes, constructor aliases, undeclared permissions, source symlinks,
global default-mux handlers and direct HTTP/TCP/TLS/other listeners remain
rejected even when they reuse an approved IPC handler. Build repeats the audit
after UI hooks. Runtime inventory checks remain authoritative for SDK routes.
Raw HTTP registrations may appear in discovery, but cannot back ctl RPC commands.
