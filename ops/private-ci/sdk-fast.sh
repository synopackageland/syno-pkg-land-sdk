#!/usr/bin/env bash
# CI uses the same basic release checks as local callers.
set -euo pipefail
exec bash "$(dirname "$0")/../../scripts/gate.sh"
