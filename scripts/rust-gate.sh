#!/usr/bin/env bash
# The Rust App SDK checks: crate syno-pkg-land-app, the thin SDK for Rust Apps
# behind the bridge, and its examples, which the Go bridge tests run.
# scripts/gate.sh runs it here, or in Docker on SYNOPKGLAND_DOCKER_SSH
# (scripts/rust-docker-gate.sh).
# SYNOPKGLAND_RUST_TOOLCHAIN: the rustup toolchain with the DSM targets (stable).
set -euo pipefail
cd "$(dirname "$0")/../rust"
cargo build --locked -p syno-pkg-land-app --examples
cargo fmt --all --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --locked
# DSM targets, as App builds use them: static musl, and armv7 glibc for the
# DSM toolkit's old glibc (musl's time64 syscalls fail on DSM's ARM kernels).
toolchain=$(dirname "$(rustup which --toolchain "${SYNOPKGLAND_RUST_TOOLCHAIN:-stable}" cargo)")
for target in x86_64-unknown-linux-musl aarch64-unknown-linux-musl armv7-unknown-linux-gnueabihf; do
	PATH="$toolchain:$PATH" cargo clippy --locked --target "$target" --all-targets -- -D warnings
done
