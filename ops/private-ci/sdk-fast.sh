#!/usr/bin/env bash
# Run native toolchain checks in the disposable runner. Old-runtime Docker
# compatibility, cross-architecture and reproducible release builds remain
# in scripts/gate.sh, the full release gate.
set -euo pipefail
cd "$(dirname "$0")/../.."
base=${SYNOPKGLAND_GATE_BASE:-origin/main}
if [[ "${GITHUB_EVENT_NAME:-}" == pull_request ]] || ! git cat-file -e "$base^{commit}" 2>/dev/null; then
  base=$(git merge-base HEAD origin/main)
fi
paths=$(git diff --name-only "$base" HEAD)
filtered=$(printf '%s\n' "$paths" | grep -vE '^(\.github/|ops/)' || true)
segments=$(printf '%s\n' "$filtered" | bash scripts/gate-plan.sh)
echo "fast gate segments: ${segments:-infrastructure only}"
want() { grep -qx "$1" <<<"$segments"; }
if want rust || want go-root; then
  (cd rust && cargo build --locked -p syno-pkg-land-app --examples)
  export SYNOPKGLAND_RUST_APP_EXAMPLES="$PWD/rust/target/debug/examples"
fi
if want go-root || want node; then
  (cd node && npm ci --no-audit --no-fund && npm run build)
fi
for pair in go-root:. go-executor:executor go-conformance:conformance; do
  want "${pair%%:*}" || continue
  (cd "${pair#*:}" && test -z "$(gofmt -l $(go list -f '{{.Dir}}' ./...))" \
    && go mod tidy -diff && go vet ./... && go test -race -count=1 ./...)
done
if want rust; then
  (cd rust && cargo fmt --all --check && cargo clippy --locked --all-targets -- -D warnings && cargo test --locked)
fi
if want python; then
  python3 -B -m unittest discover -s python/tests
fi
if want node; then (cd node && npm test); fi
if want ts; then bash scripts/ts-gate.sh; fi
echo 'Release gate retains oldest-runtime Docker, cross-architecture and reproducibility checks.'
