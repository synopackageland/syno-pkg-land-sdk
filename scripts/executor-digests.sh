#!/usr/bin/env bash
# Prints the executor digest table entry the CLI pins for this SDK version:
#   {"<version>":{"x86_64":…,"aarch64":…,"armv7":…,"lifecycle":{…},"goToolchain":…}}
# The build is the one the CLI runs: copy executor/ (a checkout or the
# module cache Dir) to a temporary directory and build there with the pinned
# toolchain and flags. Any other way of building gives other digests.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
toolchain=go1.26.10
version=${1:-$(sed -n 's/^## \([0-9][^ ]*\).*/\1/p' "$root/CHANGELOG.md" | head -n 1)}
test -n "$version" || { echo "no version: pass one or add a CHANGELOG heading" >&2; exit 1; }

if command -v sha256sum >/dev/null; then digest() { sha256sum "$1" | cut -d' ' -f1; }
else digest() { shasum -a 256 "$1" | cut -d' ' -f1; }
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/executor-digests.XXXXXX")
trap 'rm -rf "$work"' EXIT
cp -R "$root/executor" "$work/src"

build() { # family goarch [GOARM] -> sets executor and lifecycle digests
	local family=$1 goarch=$2 goarm=${3:-}
	(
		cd "$work/src"
		export CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOTOOLCHAIN="$toolchain" GOFLAGS= GOEXPERIMENT= GOWORK=off GOAMD64=v1 GOARM64=v8.0
		if test -n "$goarm"; then export GOARM="$goarm"; else unset GOARM; fi
		go build -trimpath -buildvcs=false -ldflags '-s -w -buildid=' -o "$work/$family-executor" .
		go build -trimpath -buildvcs=false -ldflags '-s -w -buildid=' -o "$work/$family-lifecycle" ./cmd/lifecycle
	)
}
build x86_64 amd64
build aarch64 arm64
build armv7 arm 7

printf '{"%s":{"x86_64":"%s","aarch64":"%s","armv7":"%s","lifecycle":{"x86_64":"%s","aarch64":"%s","armv7":"%s"},"goToolchain":"%s"}}\n' \
	"$version" \
	"$(digest "$work/x86_64-executor")" "$(digest "$work/aarch64-executor")" "$(digest "$work/armv7-executor")" \
	"$(digest "$work/x86_64-lifecycle")" "$(digest "$work/aarch64-lifecycle")" "$(digest "$work/armv7-lifecycle")" \
	"$toolchain"
