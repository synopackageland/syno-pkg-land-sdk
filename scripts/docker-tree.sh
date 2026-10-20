#!/usr/bin/env bash
# Usage: docker-tree.sh <container name> <image> <command>
# Runs command with sh in a fresh container, as uid 1000 (the SDKs refuse
# root), in /tmp/src: the tracked tree with its uncommitted changes. The tree
# goes in on stdin and nothing is bind-mounted, so the engine may be on
# another host: SYNOPKGLAND_DOCKER_SSH=<ssh host>. SYNOPKGLAND_DOCKER_TAR=<file>:
# a tar archive that goes in after the tree. SYNOPKGLAND_DOCKER_ARGS: more
# docker run arguments (volumes, environment). SYNOPKGLAND_DOCKER_SLOT=1: a
# heavy run, which waits for a gate slot on the host (scripts/docker-engine.sh).
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
. "$root/scripts/docker-engine.sh"
name=$1 image=$2 command=$3
case $name in *[!A-Za-z0-9_.-]* | '') echo "invalid container name: $name" >&2; exit 1 ;; esac
run="docker run --rm -i --name $name --user 1000:1000 -e HOME=/tmp ${SYNOPKGLAND_DOCKER_ARGS:-} $image \
	sh -c 'mkdir /tmp/src && tar -xi -C /tmp/src && cd /tmp/src && $command'"
if test "${SYNOPKGLAND_DOCKER_SLOT:-}" = 1; then run=$(slot_command "$run"); fi

cd "$root"
# git stash create records the working tree without touching the stash list.
tree=$(git stash create)
archive() {
	git archive "${tree:-HEAD}"
	if test -n "${SYNOPKGLAND_DOCKER_TAR:-}"; then cat "$SYNOPKGLAND_DOCKER_TAR"; fi
}
archive | engine "$run"
