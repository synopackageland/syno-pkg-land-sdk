# syno-pkg-land-app

Thin SDK for a Rust App behind the synopkgland Go runtime (channel
`spec/bridge/v1`). It is the Rust
counterpart of the Python (`python/`) and Node (`node/`) App SDKs and has
the same shape: `App`, `method`, `Context`, `runtime(op, params)`, `Error`.

The Go runtime (`bridge/cmd/runtime`) is the package's CGI. It checks the
origin, the DSM session, the entry gate, `permissions.json`, the request
size and the deadline, and only then starts the App, as the package account,
with the verified call on stdin. A handler never sees the browser's cookie
or SynoToken and never runs as root.

Dependencies: `serde`, `serde_json` (`raw_value`) and `libc`.

## Build and package with the CLI

Install the public CLI with `npm install --global @synopackageland/cli` and
consult the [App documentation](https://synopkgland.com/docs/) for package
setup. With access to the matching SDK sources (see below), create and build
an App:

```sh
synopkgland init -t native-rust my-app
cd my-app
synopkgland build
```

The build compiles the backend and packages the SPK. Install the Go and Rust
toolchains required by that CLI release; the Rust crate alone does not supply
the package CGI.

The CLI compiles `bridge/cmd/runtime` from the matching SDK Go module into
`api.cgi`. If executor capabilities are enabled, it also compiles the
matching executor module's executor and lifecycle helper. These Go sources
and binaries are not included in this crate or in the public CLI, frontend
SDK or Node App npm tarballs. The CLI obtains the sources with
`go mod download` for the SDK version pinned by the App.

The current CLI requires `syno-pkg-land-app` to come from a release-tagged
Git dependency in `Cargo.lock`; it rejects a crates.io dependency with
`SDK_TAG_REQUIRED`. Publishing this crate does not change that requirement.
While the SDK repository is private, building an SPK also requires authorized
Git access to its matching Rust and Go sources. A public, matching source
release and CLI support for registry dependencies are needed before an
external developer can build using only public dependencies.

This package uses the public website for its homepage and documentation;
it intentionally omits a repository URL while the source repository is private.
Documentation follows the latest SDK release.

## An App

```rust
use serde_json::json;
use syno_pkg_land_app::{App, Error};

#[derive(serde::Deserialize)]
struct Save {
    note: String,
}

fn main() -> std::io::Result<()> {
    let mut app = App::new();
    app.method("hello.greet", |ctx, _| {
        Ok(json!({"message": format!("Hello, {}", ctx.user)}))
    });
    app.method("hello.save", |_, params| {
        let p: Save = params.decode().map_err(|_| Error::bad_request("note must be a string"))?;
        Ok(json!({"bytes": p.note.len()}))
    });
    app.run()
}
```

`examples/hello.rs` is the complete App (it writes the note to the package's
data directory); `examples/golden.rs` answers the cross-language golden cases.

The package's `app/bridge.json` names the binary and lists the same methods:

```json
{"schema": 1, "runtime": "rust", "entry": "bin/hello", "mode": "exec",
 "methods": ["hello.greet", "hello.save"]}
```

The runtime registers routes from that manifest, never from the App, and
refuses the call when the App's `hello` lists other methods.
`permissions.json` sets each method's policy, as for a Go App.

## API

| Item | Meaning |
|---|---|
| `App::new()`, `app.method(name, handler)` | Registers a method; a name registered twice panics. The handler is `Fn(&Context, &Params) -> Result<T, Error>` for any `T: Serialize` |
| `app.run()` | Answers the one call on stdin and returns. Refuses to start as root, or when `SYNOPKGLAND_BRIDGE` is not `stdio` |
| `<entry> --describe`, `app.describe()` | Prints one JSON line, `{"sdk":"rust/…","versions":[1],"methods":[…]}` with the methods sorted, and exits 0, without the root or `SYNOPKGLAND_BRIDGE` checks, so the package build can check the methods against `bridge.json` and `permissions.json` |
| `app.serve(reader, writer)` | Answers one call on any byte stream; for tests |
| `ctx.user`, `ctx.admin`, `ctx.locale`, `ctx.request_id` | The caller the runtime verified. `user` is empty for a public method; `locale` is the first `Accept-Language` tag or empty |
| `ctx.deadline_ms`, `ctx.deadline()` | Time left when the call was sent, and the `Instant` it runs out |
| `ctx.cancelled()` | True once the runtime sent `cancel` (deadline or browser gone) or closed the channel. A long handler checks it and returns |
| `ctx.runtime::<T>(op, &params)` | Asks the runtime for a service and decodes the result as `T`. A refusal is `Err` with the runtime's code; a result that is not a `T` is `internal` |
| `params.get()`, `params.decode::<T>()` | The request's raw `params` JSON, or decoded as `T` (`bad_request` on mismatch) |
| `Error::new(code, message)`, `Error::bad_request(message)` | An error the browser sees as `{"success":false,"error":{"code","message"}}` |
| `refusals::*` | The upper-case refusal codes of `spec/protocol/v1/errors.json`, generated by `go generate ./bridge` |

A handler panic is answered `internal` with the message `internal error`; the
panic text goes to stderr only. A method the App does not have is `internal`
too, since the runtime only sends methods from the manifest. Build Apps with
`panic = "unwind"` (the workspace's release profile), or a panic ends the
process and the browser sees `app_unavailable`.

## Errors

- `code` is the App's own lower-case code (`^[a-z][a-z0-9_]{0,63}$`) or an
  upper-case refusal from `refusals`, for example a `SCOPE_DENIED` that a
  `delegate.*` service returned and the handler passes on with `?`. `message`
  has at most 1024 bytes.
- The runtime's own codes are reserved: `session_required`, `admin_required`,
  `app_privilege_denied`, `auth_unavailable`, `unknown_method`,
  `cross_origin`, `origin_required`, `request_timeout`, `request_canceled`,
  `request_too_large`, `json_required`, `method_not_allowed`,
  `app_unavailable`. The runtime answers `internal` for an App error that
  uses one, or that breaks the rules above, and logs it. The SDK does not
  check this itself; the runtime is the one place that decides.
- `ctx.runtime` returns `request_canceled` when the runtime closed the
  channel before answering.

## Numbers

`Params` keeps the request's JSON text, so number literals survive: `2.50`
stays `2.50` and 64-bit integers stay exact. Decode into the type you need,
or return `Box<RawValue>` (or a struct holding `&RawValue`) to echo a value
byte for byte. `ctx.runtime::<Box<RawValue>>` keeps a service result the
same way. The Python and Node SDKs give values instead (`2.50` becomes
`2.5`), so schemas shared with them should carry 64-bit IDs and sizes as
strings.

## Testing an App

Test handlers without the runtime: run `App::serve` on one end of a Unix
socket pair and speak the channel's frames from the other, as
`src/tests.rs` does.

```rust
let (rt, app_end) = std::os::unix::net::UnixStream::pair()?;
let w = app_end.try_clone()?;
std::thread::spawn(move || app.serve(app_end, w));
// write {"t":"hello","versions":[1]}, read the App's hello,
// write {"t":"call","id":"1","method":"hello.save","params":{...},"ctx":{...}},
// answer each {"t":"svc",...} with {"t":"svc_result","id":...,"ok":true,"result":...},
// then read {"t":"result",...}.
```

`ctx` must carry `user`, `admin`, `locale`, `request_id` and `deadline_ms`
(`spec/bridge/v1/frames.schema.json`). End to end, `bridge/runtime_test.go`
and `bridge/golden_test.go` run the examples behind the real runtime.

## Targets

DSM Apps are built for `x86_64-unknown-linux-musl`,
`aarch64-unknown-linux-musl` and `armv7-unknown-linux-gnueabihf` (linked with
the DSM toolkit's old glibc; musl's time64 syscalls fail on DSM's ARM
kernels). The gate runs clippy for all three.

## Development

```
cargo fmt --all --check && cargo clippy --all-targets -- -D warnings && cargo test
scripts/rust-linux-test.sh      # the tests on Linux (SYNOPKGLAND_DOCKER_SSH=<host> for a remote engine)
```

With bridge capability `webapi:true` and approved executor profiles, the existing
runtime interface offers the same service calls as Node and Python:

```rust
let grant: serde_json::Value = ctx.runtime("webapi.grant", &serde_json::json!({
    "scope": "calendar", "lifetime": "until-revoked"}))?;
let result: serde_json::Value = ctx.runtime("webapi.call", &serde_json::json!({
    "delegation_id": grant["delegation_id"], "api": "SYNO.Cal.Event",
    "method": "list", "version": 6, "params": {}}))?;
```

`webapi.grants` lists the caller's grants; `webapi.revoke` revokes one. Results
include DSM's success/error envelope. Never automatically retry an uncertain
mutation; the executor independently enforces profile, runner and permissions.

## Crate release (maintainers)

The crate version follows the SDK release. Preparation does not bump the
version or publish it. Integrate any pending bridge protocol changes before
choosing the next SDK version, and update the crate, workspace lockfile,
frontend SDK and Node App versions together.

There is currently no SDK publish orchestrator in this repository.
`scripts/release-crate.sh` supplies its crate step: run it inside a Linux
container on the configured build host, using `scripts/crate-release.Dockerfile`.
It defaults to `--dry-run`, packages and verifies the crate, scans the actual
archive with `scripts/check-internal-content.mjs`, and runs
`cargo publish --dry-run`. Set `SPL_INTERNAL_DENYLIST` to a readable,
externally mounted policy file. Pass `CARGO_REGISTRY_TOKEN` only through the
container's environment, an env-file or stdin; never put it in command arguments
or logs. Neither file belongs in the repository or build context.

After explicit release approval and the full SDK release gate, publish the
matching Go module tags and both npm packages first. Then invoke the crate
step with `--publish` from the clean, tagged SDK source checkout. It verifies
the SDK version and both public npm versions, repeats package verification
and the denylist scan, then publishes to crates.io. No GitHub Actions are used.
Finally, read back the crates.io version/checksum and record it with the npm
artifacts in `release/v<version>.json`. A dry-run does not prove that the
registry token can publish; do not publish just to test credentials.
