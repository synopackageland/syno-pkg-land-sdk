#!/usr/bin/env bash
# Build the Compose payload exactly as the CLI's buildComposeTool does.
# No target binary is executed; this is a manual release build, not a gate.
# Usage: compose-helper-digests.sh <output-directory>
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
: "${1:?Pass an output directory outside the source tree}"
mkdir -p "$1"
out=$(cd "$1" && pwd)
version=$(node -p 'require(process.argv[1]).version' "$root/node/package.json")
toolchain=go1.26.10
build() {
 local family=$1 goarch=$2
 mkdir -p "$out/$family"
 (
  cd "$root"
  export CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOTOOLCHAIN="$toolchain" GOFLAGS= GOEXPERIMENT= GOWORK=off GOAMD64=v1 GOARM64=v8.0
  unset GOARM
  for tool in plan storage-init; do
   go build -trimpath -buildvcs=false -ldflags '-s -w -buildid=' -o "$out/$family/$tool" "./compose/cmd/$tool"
  done
 )
}
build x86_64 amd64
build aarch64 arm64
node --input-type=module - "$out" "$version" "$toolchain" <<'JS'
import {readFileSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {join} from 'node:path';
const [out,sdkVersion,goToolchain]=process.argv.slice(2);
const helpers=Object.fromEntries(['x86_64','aarch64'].map(arch=>[arch,Object.fromEntries(['plan','storage-init'].map(tool=>[tool,createHash('sha256').update(readFileSync(join(out,arch,tool))).digest('hex')]))]));
const content=JSON.stringify({sdkVersion,goToolchain,helpers},null,2)+'\n';
writeFileSync(join(out,'manifest.json'),content);
process.stdout.write(content);
JS
