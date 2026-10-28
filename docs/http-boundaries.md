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
