# Synology Package Land SDK

SDK for DSM packages built with [synopkgland](https://github.com/synopackageland/syno-pkg-land-cli).

| Path | Contents |
|---|---|
| `/` | Go module `github.com/synopackageland/syno-pkg-land-sdk`: `api`, `appbackup`, `background`, `datamigrate`, `delegate`, `dsm`, `filestation`, `identity`, `ipc`, `jsonlimits`, `portal`, `sharedfolder` |
| `executor/` | Go module `github.com/synopackageland/syno-pkg-land-sdk/executor`: the delegated executor; pins the root Go SDK for the shared DSM role provider |
| `ts/` | npm package `@synopackageland/sdk` (`.` headless, `./vue` components, `./native`, `./entry`, `./mobile`, `./mobile/shell`) |
| `spec/protocol/v1/` | Executor protocol: rules (RFC 2119), op schemas, `errors.json`, vectors |
| `spec/delegate/v1/` | App backend delegate DTOs: single schema generating Go and TypeScript wire types |
| `bridge/`, `spec/bridge/v1/` | `bridge/cmd/runtime`, the package CGI that hands verified calls to a Python, Node or Rust App, one process per request; the channel and the App manifest (`bridge.json`) are frozen ([spec/bridge/v1](spec/bridge/v1/README.md), [docs/multilang/design.md](docs/multilang/design.md)). Each request starts the App: Node adds about 0.5 s on an ARM NAS, so latency-sensitive Apps should use Python or Rust |
| `rust/app/` | Crate `syno-pkg-land-app`: thin SDK for Rust Apps behind the bridge runtime ([rust/app/README.md](rust/app/README.md)) |
| `node/` | npm package `@synopackageland/app`: the Node App SDK for the bridge, TypeScript with ESM and `.d.ts`, Node 22 or later, no runtime dependencies ([node/README.md](node/README.md)) |
| `python/` | Package `synopkgland_app`: the Python App SDK for the bridge, standard library only, Python 3.8 or later ([python/README.md](python/README.md)) |
| `testdata/golden/` | `api.Server` replies every bridge App must reproduce byte for byte (repository check, not published) |
| `supervision/` | Shared bounded restart policy, dependency waits and Linux process-group ownership |
| `conformance/` | Runs the vectors against the executor over a Unix socket (repository check, not published) |
| `scripts/` | `gate.sh` (local gate), `executor-digests.sh` (reproducible SDK payload inventory) |

`<sdk-version>` is a placeholder, not manifest syntax. `synopkgland init` copies compatible SDK pins from the locally installed templates. For a manual configuration, query `npm view @synopackageland/sdk version`, replace the placeholder with that exact release, and keep all App SDK declarations (including Go, Rust and frontend dependencies) aligned. Run `synopkgland check` with an up-to-date CLI before building.

An app depends on the SDK through `backend/go.mod`:

```
require github.com/synopackageland/syno-pkg-land-sdk v<sdk-version>
```

A Rust App depends on `syno-pkg-land-app` (`rust/app/`) and runs behind the
Go runtime:

```
syno-pkg-land-app = { git = "https://github.com/synopackageland/syno-pkg-land-sdk", tag = "v<sdk-version>" }
```

The `go.mod` line is the only source of the SDK version. The CLI builds the executor
from `github.com/synopackageland/syno-pkg-land-sdk/executor` at the immutable
release declared by the SDK executor manifest and verifies its six payload digests. The manifest ships in both the Go module
and npm package. Use a CLI that supports executor manifest format 1 and
templates that produce the owner-and-grant staging/job namespaces required by
`delegate.OwnerStagingPrefix` and `delegate.OwnerJobSuffix`. The CLI builds the
Hyper Backup control from `appbackup`.

Apps build their API server with
`api.NewServerWithPermissions(auth, permissionsJSON, routes...)`; it refuses
an SDK policy that differs from the digest the CLI binds with
`-X github.com/synopackageland/syno-pkg-land-sdk/api.expectedSDKPermissionsDigest=…`.

For bounded public HTTP(S) subprocess probes, see [Outbound probe proxy](docs/outbound-probe-proxy.md).

For optional HTTPS requirements, administrator-only HTTPS links and opt-in
read RPC recovery after a stale DSM token, see
[HTTPS and session recovery](docs/https-and-session-recovery.md).
Ordinary HTTP Apps remain supported; writes, AI and streams never auto-replay.

For owner-bound asynchronous Go work, structured results and ctl integration, see
[Structured background jobs](docs/background-results.md).

Native Apps appear in Hyper Backup as applications by default; see
[Hyper Backup application backup](docs/app-backup.md) for what is archived,
offline and online modes, the data version check, and the fixed root script
Hyper Backup runs.

## Testing an App with the executor

An App behind the bridge runtime reaches its executor at
`/run/synopkgland/<package>/executor.sock`, which only root can create. A Go
end-to-end test serves a fake executor instead, without root and on macOS
too:

```go
e := delegatetest.NewExecutor(t, func(q delegate.Request) string {
	if q.Op == "root.run" {
		return delegatetest.OK(`{"request_id":"r1","exit_code":0,"stderr":"","output":"{}"}`)
	}
	return delegatetest.Refused("EXECUTOR_PAUSED")
})
h, err := bridge.NewRuntime(bridge.Config{PackageID: "MyApp", AppDir: appDir,
	DataDir: t.TempDir(), Auth: auth, ExecutorSocket: e.Socket})
// serve requests with h, then check e.Requests()
```

The fake speaks protocol v1's framing only: it checks no session and no
policy. `ExecutorSocket` is for tests: the package CGI and worker
(`bridge/cmd/runtime`) never set it, so no request can choose the socket.

Read DSM service and port definitions without root with [dsmservices](docs/dsm-service-ports.md).

## Development

For ES module frontends and lazy routes in DSM windows, see
[frontend bundles](docs/frontend-bundles.md).

Native foreground daemons can opt into bounded crash recovery in their generated
runtime config; see [native daemon restart policy](docs/native-daemon-restart.md)
for defaults, status diagnostics and the required CLI/templates integration.

```sh
npm run gates                  # basic checks for development and release
npm run gates:full             # extended checks, only when explicitly requested
bash scripts/gate.sh --plan    # print the default workloads without running them
go generate ./...              # after editing protocol or delegate specs
```

Basic checks cover core unit tests, native builds, static policy and manifest
validation, npm packing and tarball content scans. Full adds target matrices,
runtime containers, daemon/gateway integration, Rust target checks, cold
executor reproducibility and registry integration. Full is optional for releases.
See [release checks](docs/release-checks.md) for all entry points.
A configured `SYNOPKGLAND_DOCKER_SSH` supplies build isolation for frontend checks;
it does not enable extended integrations. Rust runs only in Linux containers.

### Contributor checks

Local release checks and private CI share the basic gate. From a clone with
dependencies accessible, install Git, Bash and the Go toolchain declared in
`go.mod`, then run:

```sh
git fetch origin
npm run gates
```

Basic checks need Node.js/npm, Go and Python. Extended checks additionally use
`uv`, Rust/rustup and Docker for supported runtime and target checks. Rust runs
inside a Linux container. A release uses basic checks; extended checks require
`npm run gates:full`.
Neither gate requires a NAS or DSM login.
The root module's Compose prepare tests read Linux kernel routes from `/proc`;
run those checks on Linux or in a Linux container. Docker SSH redirects the
Rust/TS segments; root Go checks still run on the machine invoking the gate.

Docker SSH is optional. Contributors can use their own local tools and Docker
engine with `SYNOPKGLAND_DOCKER_SSH` unset, or an SSH-accessible Docker host:

```sh
SYNOPKGLAND_DOCKER_SSH=builder.example npm run gates
```

`builder.example` is a placeholder; supply your own host. Set
`SYNOPKGLAND_GATE_BASE` to another fetched ref when comparing a different base.
On a shared host, keep the `synopkgland-` gate images and cache volumes; remove
only containers and temporary files created by your run.

Executor source or build-input changes require a matching immutable inventory.
Build it only as `executor-digests.sh` specifies; other build inputs can change
the resulting digests. Unrelated patches reuse the committed inventory.

License: MIT for SDK code; retained upstream Compose fixtures have separate
[licenses and notices](compose/testdata/README.md).

## Notifications and App entry

Use [`notify`](docs/notifications.md) for stable notification delivery identities,
event/key throttling and App-owned webhook/SMTP attempts. The App retains its
durable outbox and retry policy.

[`App entry policy`](docs/app-entry.md) covers the centralized CGI admin gate
and the non-blocking frontend `EntryGuard`.

Use [`dsm.LookupAccount`](docs/native-account.md) for a complete single-account
snapshot, provider source, numeric groups and distinct lookup errors.

## Identity gateway

See [gateway access failures](docs/gateway-access.md) for admission errors and
localization, and [DSM-managed HTTPS ingress](docs/gateway-ingress-design.md) for the
native transport, certificate trust requirements and direct fallback.

## Upstream Compose

See [Compose packages](docs/compose.md) for schema-2 planning, identity gateway transport, immutable artifacts and explicit DSM acceptance blockers.

## Described commands

[Syno Package Land API 描述 v1](spec/api/v1/README.md) defines authenticated discovery, explicit task-level commands, fixed workflow adapters, and the public JSON Schema. `api.WithDescription` serves the private build artifact using DSM authentication; `api.DescribeRoutes` exports the actual registry to the build tool.

See [runtime storage and NAS idle periods](docs/idle-storage.md) for volatile worker liveness and durable job/approval state.

For public-network URL downloads and registry access, see [outbound HTTP policy](docs/outbound-http.md).

For credential-authorized IPC between package accounts, use
[`ipc.StartPackagePeerUnix`](docs/package-peer-ipc.md).

## Single operation registry

Native Apps declare operation metadata in a `synopkgland.api` v2 JSON registry.
The CLI generates permissions, runtime bindings, the private API descriptor,
ctl commands, AI/MCP tools and the App reference from that one source. English
summaries are not maintained in a second skill or document. Each operation needs
an explicit `ai` class: read, preview, draft, human-only or hidden. Reads and
previews cannot mutate; drafts require a non-mutating preview and human
confirmation. Long operations use a fixed job flow.

Go Apps embed the generated full registry and construct the server with
`api.NewServerWithOperations`; `ValidateOperationRegistry` checks exact handler,
spec and permission equality before handlers or migrations run. Only the fixed
describe and identity ceremony routes are infrastructure exceptions. SDK-owned
capability metadata lives in `api/operations.json` and defaults to hidden.

The bridge exporter `bridge/cmd/describe` constructs its actual route set without
starting an App. Python, Node and Rust share the existing bridge runtime: the
CLI binds the digest of generated `synopkgland-operations.json`, and startup
refuses altered metadata or route/spec inequality before dispatch. No new
transport, proxy or service is introduced.

Run `synopkgland ctl generate`, `synopkgland ctl generate --check` and
`synopkgland check` before building with matching SDK/CLI/templates releases.
OpenAPI imports use the same v2 format; every imported operation is hidden until
reviewed per API. Wrapper-owned specs and adapters stay under `synology/`.

External-service passwords and tokens should use the [request-only Secret contract](docs/request-secrets.md), including its persistence and diagnostic boundaries.

Private native audio/video playback is available through the Go `media` service
and TypeScript `mediaUrl` helper; see [media grants](docs/media.md) for owner hooks,
session binding, renewal, and Range handling.

Full-page Apps use [`@synopackageland/sdk/entry`](docs/entry-page.md) for boot,
shared context and sign-in display caches; Go Apps can expose language fields
with `api.AppStatusHandler`. Native mobile Apps use
[`@synopackageland/sdk/mobile`](spec/native/v1/README.md) for actions and browser
fallbacks; shell implementations use the DOM-free `./mobile/shell` contract.
