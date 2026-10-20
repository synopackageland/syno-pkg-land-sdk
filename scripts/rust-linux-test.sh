#!/usr/bin/env bash
# Runs the Rust App SDK tests on x86_64 Linux in Docker, as on DSM
# (scripts/docker-tree.sh).
set -euo pipefail
exec "$(dirname "$0")/docker-tree.sh" "${SYNOPKGLAND_RUST_TEST_CONTAINER:-syno-pkg-land-sdk-rust-test}" \
	public.ecr.aws/docker/library/rust:1.97.1-bookworm \
	'cd rust && CARGO_HOME=/tmp/cargo cargo test --locked -q'
