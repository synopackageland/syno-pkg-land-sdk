#!/usr/bin/env bash
# Tests of the gate scripts' own logic. scripts/gate.sh runs it first.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
. "$root/scripts/docker-engine.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failed=0
check() { # name want got
	if test "$2" != "$3"; then
		printf '%s:\n  want: %s\n  got:  %s\n' "$1" "$2" "$3" >&2
		failed=1
	fi
}

# The tag is the first 12 hex digits of the Dockerfile's SHA-256.
printf 'FROM scratch\n' >"$work/Dockerfile"
check "gate image tag" synopkgland-gate-sdk:bb57c7da220a "$(gate_image_name sdk "$work/Dockerfile")"

# slot_command: a fake flock on PATH. Slot 0 is busy, slot 1 is free.
mkdir "$work/bin"
cat >"$work/bin/flock" <<'EOF'
#!/bin/sh
# flock -n -E 75 <lock> <command...>
lock=$4; shift 4
echo "$lock" >>"$FLOCK_LOG"
case $lock in *slot0.lock) exit 75 ;; esac
exec "$@"
EOF
chmod +x "$work/bin/flock"
got=$(FLOCK_LOG="$work/log" PATH="$work/bin:$PATH" sh -c "$(slot_command 'echo ran; exit 3')" 2>&1) && status=0 || status=$?
check "slot command output" ran "$got"
check "slot command status" 3 "$status"
check "slot command tries slot 0, then slot 1" \
	"/tmp/synopkgland-gate.slot0.lock /tmp/synopkgland-gate.slot1.lock" "$(tr '\n' ' ' <"$work/log" | sed 's/ $//')"

# ts-gate.sh on SYNOPKGLAND_DOCKER_SSH: in Docker on that host, in a gate slot,
# in Node.js 24.20.0's image. A fake ssh logs the host's commands.
cat >"$work/bin/ssh" <<'EOF'
#!/bin/sh
shift
printf '%s\n' "$*" >>"$SSH_LOG"
cat >/dev/null
EOF
chmod +x "$work/bin/ssh"
SSH_LOG="$work/ssh.log" VITEST_MAX_WORKERS= PATH="$work/bin:$PATH" SYNOPKGLAND_DOCKER_SSH=host SYNOPKGLAND_CONTAINER_PREFIX=t \
	SYNOPKGLAND_DOCKER_NPM_REGISTRY=https://mirror.invalid/ "$root/scripts/ts-gate.sh" </dev/null
run=$(grep -e '--name t-ts-gate' "$work/ssh.log")
has() { case $run in *"$1"*) echo yes ;; *) echo "no: $1" ;; esac; }
check "ts in Docker: a gate slot" yes "$(has 'synopkgland-gate.slot$slot.lock')"
check "ts in Docker: Node.js 24.20.0" yes "$(has 'library/node:24.20.0-bookworm@sha256:')"
check "ts in Docker: the shared npm cache" yes "$(has '-v synopkgland-cache-npm:/cache/npm -e npm_config_cache=/cache/npm')"
check "ts in Docker: the registry" yes "$(has '-e npm_config_registry=https://mirror.invalid/')"
check "ts in Docker: vitest takes half the host's CPUs" yes "$(has '-e VITEST_MAX_WORKERS=$(($(nproc) / 2))')"
check "ts in Docker: ts-gate.sh in a committed tree" yes "$(has 'commit -qm tree && bash scripts/ts-gate.sh')"

# A caller can bound vitest independently of the remote host's CPU count.
: >"$work/ssh.log"
SSH_LOG="$work/ssh.log" PATH="$work/bin:$PATH" SYNOPKGLAND_DOCKER_SSH=host SYNOPKGLAND_CONTAINER_PREFIX=t \
 VITEST_MAX_WORKERS=2 "$root/scripts/ts-gate.sh" </dev/null
run=$(grep -e '--name t-ts-gate' "$work/ssh.log")
check "ts in Docker: caller's vitest workers" yes "$(has '-e VITEST_MAX_WORKERS=2 ')"

# Installing an unpublished UI tarball starts with the locked dependencies.
cat >"$work/bin/npm" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$NPM_LOG"
case "$1" in run) exit 42 ;; esac
EOF
chmod +x "$work/bin/npm"
NPM_LOG="$work/npm.log" PATH="$work/bin:$PATH" SYNOPKGLAND_DOCKER_SSH= \
 SYNOPKGLAND_UI_TARBALL=/tmp/ui.tgz "$root/scripts/ts-gate.sh" && result=0 || result=$?
check "ts tarball: reaches typecheck" 42 "$result"
check "ts tarball: install order" "ci install run" "$(awk '{print $1}' "$work/npm.log" | tr '\n' ' ' | sed 's/ $//')"

# gate-plan.sh: changed paths in, the fast gate's segments out.
plan() { printf '%s\n' "$@" | "$root/scripts/gate-plan.sh" | tr '\n' ' ' | sed 's/ $//'; }
all="go-conformance go-executor go-root node python rust ts"
check "plan ts" ts "$(plan ts/src/index.ts)"
check "plan delegate DTO spec" "go-root ts" "$(plan spec/delegate/v1/dtos.schema.json)"
check "plan delegate DTO generator" "go-root ts" "$(plan internal/delegategen/main.go)"
check "plan delegate DTO TS artifact: Go checks it" "go-root ts" "$(plan ts/src/delegate/dtos-gen.ts)"
check "plan rust" rust "$(plan rust/app/src/lib.rs)"
check "plan python: the bridge tests run its examples" "go-root python" "$(plan python/synopkgland_app/app.py)"
check "plan node: the bridge tests run its examples" "go-root node" "$(plan node/src/index.ts)"
check "plan executor: conformance reads it" "go-conformance go-executor" "$(plan executor/executor.go)"
check "plan conformance" go-conformance "$(plan conformance/vectors_test.go)"
check "plan delegate: conformance reads it" "go-conformance go-root" "$(plan delegate/client.go)"
check "plan dsm/native: the executor reads it" "go-executor go-root" "$(plan dsm/native/apppriv-x.elf)"
check "plan root module" go-root "$(plan api/server.go go.sum testdata/golden/api.jsonl)"
check "plan spec: every SDK reads it" "$all" "$(plan spec/protocol/v1/errors.json)"
check "plan scripts: the gate itself" "$all" "$(plan scripts/gate.sh)"
check "plan docs" "" "$(plan README.md CHANGELOG.md LICENSE docs/multilang/design.md release/v0.10.3.json)"
check "plan nothing" "" "$(plan)"
check "plan several" "go-conformance go-executor go-root ts" "$(plan ts/a.ts executor/a.go api/a.go)"

exit $failed
