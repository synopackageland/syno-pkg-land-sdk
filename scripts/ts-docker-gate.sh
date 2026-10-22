#!/usr/bin/env bash
# Runs scripts/ts-gate.sh on x86_64 Linux in Docker (scripts/docker-tree.sh),
# in a gate slot, so this machine's load cannot time out its tests. The image
# is Node.js 24.22.1's, the version of the synopkgland frontend gate image;
# node_modules is installed there, with the npm cache in the host's shared
# synopkgland-cache-npm volume. ts-gate.sh lists spec/ with git, so the tree is
# committed to a fresh repository there. SYNOPKGLAND_UI_TARBALL follows that tree.
# SYNOPKGLAND_DOCKER_NPM_REGISTRY selects an alternate package registry.
# The host CA bundle is available for TLS certificate validation.
# vitest takes half the host's CPUs (VITEST_MAX_WORKERS): the host's other gate
# slot has the rest.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
. "$root/scripts/docker-engine.sh"
prefix=${SYNOPKGLAND_CONTAINER_PREFIX:-syno-pkg-land-sdk}
registry=${SYNOPKGLAND_DOCKER_NPM_REGISTRY:-}
case $registry in *[!A-Za-z0-9:/._-]*) echo "invalid SYNOPKGLAND_DOCKER_NPM_REGISTRY: $registry" >&2; exit 2 ;; esac
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

workers=${VITEST_MAX_WORKERS:-}
case $workers in *[!0-9]*) echo "invalid VITEST_MAX_WORKERS: $workers" >&2; exit 2 ;; esac
workers=${workers:-'$(($(nproc) / 2))'}
args="-e VITEST_MAX_WORKERS=$workers -v synopkgland-cache-npm:/cache/npm -e npm_config_cache=/cache/npm ${registry:+-e npm_config_registry=$registry}"
if test -n "${SYNOPKGLAND_UI_TARBALL:-}"; then
	mkdir "$work/ui"
	cp "$SYNOPKGLAND_UI_TARBALL" "$work/ui/ui.tgz"
	tar -c -f "$work/ui.tar" -C "$work/ui" ui.tgz
	export SYNOPKGLAND_DOCKER_TAR="$work/ui.tar"
	args="$args -e SYNOPKGLAND_UI_TARBALL=/tmp/src/ui.tgz"
fi
# The CA bundle is mounted only when the host has one.
ca=/etc/ssl/certs/ca-certificates.crt
args="$args \$(test -f $ca && echo -v $ca:/tmp/host-ca.crt:ro -e NODE_EXTRA_CA_CERTS=/tmp/host-ca.crt)"

image=public.ecr.aws/docker/library/node:24.22.1-bookworm@sha256:5630d96570d4ca4a8523fda3d169d0114554d0f48293e1ae3db6be7c79b42a17
cache_volumes "$image" synopkgland-cache-npm
SYNOPKGLAND_DOCKER_SLOT=1 SYNOPKGLAND_DOCKER_ARGS="$args" \
"$root/scripts/docker-tree.sh" "${SYNOPKGLAND_TS_GATE_CONTAINER:-$prefix-ts-gate}" "$image" \
	'git init -q && git add -A && git -c user.name=gate -c user.email=gate@invalid commit -qm tree && bash scripts/ts-gate.sh'
