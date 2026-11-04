#!/usr/bin/env bash
# Basic release checks by default; SPL_FULL_GATE=1 aliases --full.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
mode=basic
[[ ${SPL_FULL_GATE:-0} != 1 ]] || mode=full
for arg in "$@"; do
  case "$arg" in --full) mode=full ;; --fast) ;; --plan) ;; *) echo 'usage: gate.sh [--full|--fast] [--plan]' >&2; exit 2 ;; esac
done
plan=$(node "$root/scripts/release-gate-plan.mjs" "$mode")
if [[ " $* " == *' --plan '* ]]; then printf '%s\n' "$plan"; exit; fi
while read -r segment; do
  case "$segment" in
    basic) bash "$root/scripts/basic-gate.sh" ;;
    integrations) bash "$root/scripts/gate-full.sh" ;;
    registry) python3 "$root/scripts/test-npm-publish.py" ;;
  esac
done <<< "$plan"
