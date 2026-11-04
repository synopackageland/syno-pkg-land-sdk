# Rust

Cargo workspace with one crate: `syno-pkg-land-app` (`app/`), the thin SDK
for a Rust App behind the Go bridge runtime ([app/README.md](app/README.md)).

A Rust App serves its methods
through the Go runtime like a Python or Node App, and asks the runtime for
services (`ctx.runtime("delegate.…")`) instead of linking its own copy of
the DSM checks.

```
cargo fmt --all --check && cargo clippy --all-targets -- -D warnings && cargo test
scripts/rust-linux-test.sh      # the tests on Linux in Docker (SYNOPKGLAND_DOCKER_SSH=<host> for a remote engine)
```
