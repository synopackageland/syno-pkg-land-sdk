# Synology Package Land SDK

SDK for DSM packages built with [synopkgland](https://github.com/synopackageland/syno-pkg-land-cli).

| Path | Contents |
|---|---|
| `/` | Go module `github.com/synopackageland/syno-pkg-land-sdk`: `api`, `background`, `datamigrate`, `delegate`, `dsm`, `filestation`, `identity`, `jsonlimits`, `portal`, `sharedfolder` |
| `executor/` | Go module `github.com/synopackageland/syno-pkg-land-sdk/executor`: the delegated executor; pins the root Go SDK for the shared DSM role provider |
| `ts/` | npm package `@synopackageland/sdk` (`.` headless, `./vue` components) |
| `spec/protocol/v1/` | Executor protocol: rules (RFC 2119), op schemas, `errors.json`, vectors |
| `spec/delegate/v1/` | App backend delegate DTOs: single schema generating Go and TypeScript wire types |
| `bridge/`, `spec/bridge/v1/` | `bridge/cmd/runtime`, the package CGI that hands verified calls to a Python, Node or Rust App, one process per request; the channel and the App manifest (`bridge.json`) are frozen ([spec/bridge/v1](spec/bridge/v1/README.md), [docs/multilang/design.md](docs/multilang/design.md)). Each request starts the App: Node adds about 0.5 s on an ARM NAS, so latency-sensitive Apps should use Python or Rust |
| `rust/app/` | Crate `syno-pkg-land-app`: thin SDK for Rust Apps behind the bridge runtime ([rust/app/README.md](rust/app/README.md)) |
| `node/` | npm package `@synopackageland/app`: the Node App SDK for the bridge, TypeScript with ESM and `.d.ts`, Node 22 or later, no runtime dependencies ([node/README.md](node/README.md)) |
| `python/` | Package `synopkgland_app`: the Python App SDK for the bridge, standard library only, Python 3.8 or later ([python/README.md](python/README.md)) |
| `testdata/golden/` | `api.Server` replies every bridge App must reproduce byte for byte (repository check, not published) |
| `conformance/` | Runs the vectors against the executor over a Unix socket (repository check, not published) |
| `scripts/` | `gate.sh` (local gate), `executor-digests.sh` (reproducible SDK payload inventory) |

An app depends on the SDK through `backend/go.mod`:

```
require github.com/synopackageland/syno-pkg-land-sdk v0.28.0
```

A Rust App depends on `syno-pkg-land-app` (`rust/app/`) and runs behind the
Go runtime:

```
syno-pkg-land-app = { git = "https://github.com/synopackageland/syno-pkg-land-sdk", tag = "v0.28.0" }
```

The `go.mod` line is the only source of the SDK version. The CLI builds the executor
from `github.com/synopackageland/syno-pkg-land-sdk/executor` at the immutable
release declared by the SDK executor manifest. SDK 0.28.0 reuses executor/v0.27.0
and its six verified payload digests. The manifest ships in both the Go module
and npm package; compatible SDK minor releases do not require a CLI release.

Apps build their API server with
`api.NewServerWithPermissions(auth, permissionsJSON, routes...)`; it refuses
an SDK policy that differs from the digest the CLI binds with
`-X github.com/synopackageland/syno-pkg-land-sdk/api.expectedSDKPermissionsDigest=…`.

For owner-bound asynchronous Go work, structured results and ctl integration, see
[Structured background jobs](docs/background-results.md).

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

## Development

Native foreground daemons can opt into bounded crash recovery in their generated
runtime config; see [native daemon restart policy](docs/native-daemon-restart.md)
for defaults, status diagnostics and the required CLI/templates integration.

```
scripts/gate.sh                 # go, executor, conformance, bridge App SDKs, rust/app, ts, 3 arches, digests
scripts/gate.sh --fast          # only what the changes since origin/main reach (scripts/gate-plan.sh)
scripts/rust-linux-test.sh      # rust/app tests on Linux in Docker (gate runs it off Linux)
go generate ./...               # after editing spec/protocol/v1 or spec/delegate/v1
scripts/executor-digests.sh     # digest table entry for the CLI
```

Use `--fast` while developing: it prints the segments it skips, and checks
the executor digests' reproducibility only in the full gate. Run the full gate
once before a release. With `SYNOPKGLAND_DOCKER_SSH=<ssh host>` the Rust
checks run on that Docker host in `synopkgland-gate-sdk:<hash>`
(`scripts/gate.Dockerfile`), with the host's shared `synopkgland-cache-*`
volumes, and wait for one of its two gate slots. The ts segment
(`scripts/ts-gate.sh`: npm ci, vue-tsc, vitest, build, pack) runs there too,
in Node.js 24.22.1's image with the shared npm cache
(`scripts/ts-docker-gate.sh`), so this machine's load cannot time out its
tests; set `SYNOPKGLAND_DOCKER_NPM_REGISTRY` when that host cannot reach
registry.npmjs.org. Without `SYNOPKGLAND_DOCKER_SSH` everything runs here.

### Contributor checks

Checks run through the scripts above; this repository does not use GitHub
Actions. From a clone with dependencies accessible, install Git, Bash and the
Go toolchain in `go.mod`, then fetch the comparison base and run the fast gate:

```sh
git fetch origin
scripts/gate.sh --fast
```

The selected segments print before they run. Install the tools for those
segments: Node.js/npm for `node`, `ts` and the root Go module's bridge tests;
Python 3 and `uv` for Python checks; Rust/rustup with the targets in
`scripts/rust-gate.sh` for Rust checks; and Docker for the Linux/runtime checks
(including Rust on macOS, Node 22 and Python 3.8). A release uses
`scripts/gate.sh` without `--fast`. Neither gate requires a NAS or DSM login.
The root module's Compose prepare tests read Linux kernel routes from `/proc`;
run those checks on Linux or in a Linux container. Docker SSH redirects the
Rust/TS segments; root Go checks still run on the machine invoking the gate.

Docker SSH is optional. Contributors can use their own local tools and Docker
engine with `SYNOPKGLAND_DOCKER_SSH` unset, or an SSH-accessible Docker host:

```sh
SYNOPKGLAND_DOCKER_SSH=builder.example scripts/gate.sh --fast
```

`builder.example` is a placeholder; supply your own host. Set
`SYNOPKGLAND_GATE_BASE` to another fetched ref when comparing a different base.
On a shared host, keep the `synopkgland-` gate images and cache volumes; remove
only containers and temporary files created by your run.

The executor digest changes only when `executor/` changes. Build it only as
`executor-digests.sh` does (a copy of `executor/`, pinned go1.26.10); any other
build gives other digests.

License: MIT for SDK code; retained upstream Compose fixtures have separate
[licenses and notices](compose/testdata/README.md).

## Identity gateway

See [gateway access failures](docs/gateway-access.md) for admission errors and
localization, and [DSM-managed HTTPS ingress](docs/gateway-ingress-design.md) for the
native transport, certificate trust requirements and direct fallback.

## Upstream Compose

See [Compose packages](docs/compose.md) for schema-2 planning, identity gateway transport, immutable artifacts and explicit DSM acceptance blockers.

## Described commands

[Syno Package Land API 描述 v1](spec/api/v1/README.md) defines authenticated discovery, explicit task-level commands, fixed workflow adapters, and the public JSON Schema. `api.WithDescription` serves the private build artifact using DSM authentication; `api.DescribeRoutes` exports the actual registry to the build tool.

See [runtime storage and NAS idle periods](docs/idle-storage.md) for volatile worker liveness and durable job/approval state.
