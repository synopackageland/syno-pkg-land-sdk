#!/usr/bin/env bash
# Runs scripts/rust-gate.sh on x86_64 Linux in Docker (scripts/docker-tree.sh),
# then the Go bridge tests that run its examples (golden, hello) next to them:
# the examples are Linux binaries. The other Go tests skip them.
# The image is scripts/gate.Dockerfile's; CARGO_HOME (/cache/cargo, as in the
# CLI gate, with cargo's lock) and the Go module and build caches are the host's
# shared synopkgland-cache-* volumes.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
. "$root/scripts/docker-engine.sh"
prefix=${SYNOPKGLAND_CONTAINER_PREFIX:-syno-pkg-land-sdk}
examples=$(mktemp -d)
trap 'rm -rf "$examples"' EXIT

image=$(gate_image sdk "$root/scripts/gate.Dockerfile")
cache_volumes "$image" synopkgland-cache-cargo
# Its output goes to stderr: stdout carries the examples.
SYNOPKGLAND_DOCKER_SLOT=1 \
SYNOPKGLAND_DOCKER_ARGS="-v synopkgland-cache-cargo:/cache/cargo -e CARGO_HOME=/cache/cargo" \
"$root/scripts/docker-tree.sh" "${SYNOPKGLAND_RUST_GATE_CONTAINER:-$prefix-rust-gate}" "$image" \
	"{ SYNOPKGLAND_RUST_TOOLCHAIN=1.97.1 bash scripts/rust-gate.sh; } >&2 && \
tar -c rust/target/debug/examples/golden rust/target/debug/examples/hello" >"$examples/examples.tar"

golang=public.ecr.aws/docker/library/golang:1.26.9-bookworm
cache_volumes "$golang" synopkgland-cache-gomod synopkgland-cache-gobuild
SYNOPKGLAND_DOCKER_TAR="$examples/examples.tar" \
SYNOPKGLAND_DOCKER_ARGS="-v synopkgland-cache-gomod:/cache/synopkgland-cache-gomod -e GOMODCACHE=/cache/synopkgland-cache-gomod \
-v synopkgland-cache-gobuild:/cache/synopkgland-cache-gobuild -e GOCACHE=/cache/synopkgland-cache-gobuild" \
"$root/scripts/docker-tree.sh" "${SYNOPKGLAND_RUST_BRIDGE_CONTAINER:-$prefix-rust-bridge}" "$golang" \
	'SYNOPKGLAND_RUST_APP_EXAMPLES=/tmp/src/rust/target/debug/examples go test -race -count=1 -run "^(TestGoldenRepliesMatchInEveryLanguage|TestHelloExamples|TestBatchAdaptersUseOneAppAndIsolateEachResult)$/^rust$" -v ./bridge'
