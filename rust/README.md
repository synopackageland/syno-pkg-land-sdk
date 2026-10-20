# Rust

Cargo workspace with one crate: `syno-pkg-land-app` (`app/`), the thin SDK
for a Rust App behind the Go bridge runtime ([app/README.md](app/README.md)).

The Rust full SDK (crate `syno-pkg-land-sdk`, a port of the Go packages) is
gone; its last release is tag `v0.9.9`. A Rust App now serves its methods
through the Go runtime like a Python or Node App, and asks the runtime for
services (`ctx.runtime("delegate.…")`) instead of linking its own copy of
the DSM checks.

```
cargo fmt --all --check && cargo clippy --all-targets -- -D warnings && cargo test
scripts/rust-linux-test.sh      # the tests on Linux in Docker (SYNOPKGLAND_DOCKER_SSH=<host> for a remote engine)
```
