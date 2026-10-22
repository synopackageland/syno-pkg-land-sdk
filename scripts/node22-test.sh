#!/usr/bin/env bash
# Runs @synopackageland/app on the supported Node.js 22 line (DSM's Node.js_v22), in Docker: its unit tests, then the bridge's Node golden and
# hello tests (a Go test binary built here) against the package built there.
# The tracked tree, with its uncommitted changes, and node/node_modules (npm ci
# first; plain JavaScript, so any platform's will do) go in on stdin and
# nothing is bind-mounted, so the engine may be on another host and needs no
# registry access: SYNOPKGLAND_DOCKER_SSH=<ssh host>.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
. "$root/scripts/docker-engine.sh"
image=public.ecr.aws/docker/library/node:22.23.4-bookworm-slim
name=${SYNOPKGLAND_NODE_TEST_CONTAINER:-syno-pkg-land-sdk-node22-test}
case $name in *[!A-Za-z0-9_.-]* | '') echo "invalid container name: $name" >&2; exit 1 ;; esac
# Not root: the SDK refuses to run as root.
run="docker run --rm -i --name $name --user 1000:1000 -e HOME=/tmp $image \
	sh -c 'mkdir /tmp/src && tar -x -C /tmp/src && sh -e /tmp/src/node22.sh'"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$root"
# git stash create records the working tree without touching the stash list.
# Rebuilt files may have new mtimes but identical content. Refresh stat data
# first so stash does not mistake those for an empty set of changes.
git update-index -q --refresh || true
tree=$(git stash create)
git archive "${tree:-HEAD}" | tar -x -C "$work"
test -d node/node_modules || { echo "node/node_modules: run npm ci in node/ first" >&2; exit 1; }
cp -R node/node_modules "$work/node/"
arch=$(engine "docker version --format '{{.Server.Arch}}'")
CGO_ENABLED=0 GOOS=linux GOARCH=$arch go test -c -o "$work/bridge/bridge.test" ./bridge
cat >"$work/node22.sh" <<'EOF'
cd /tmp/src/node
node --version
npm test
npm run build
cd /tmp/src/bridge
./bridge.test -test.count=1 -test.v -test.run '^(TestGoldenRepliesMatchInEveryLanguage|TestHelloExamples|TestBatchAdaptersUseOneAppAndIsolateEachResult)$/^node$'
EOF
# Without the macOS extended attributes, which GNU tar warns about.
COPYFILE_DISABLE=1 tar --no-xattrs -c -C "$work" . | engine "$run"
