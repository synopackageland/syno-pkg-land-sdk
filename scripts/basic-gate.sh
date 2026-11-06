#!/usr/bin/env bash
# Native unit checks and package builds; no runtime containers or target matrix.
set -euo pipefail
cd "$(dirname "$0")/.."
npm run check:docs
node --test scripts/check-internal-content.test.mjs scripts/sdk-version.test.mjs scripts/executor-manifest.test.mjs scripts/release-gate-plan.test.mjs scripts/check-timeout-policy.test.mjs
node scripts/check-timeout-policy.mjs
node scripts/sdk-version.mjs --check
node scripts/executor-manifest.mjs --check
node scripts/check-internal-content.mjs --repo .
(cd node && npm ci --no-audit --no-fund)
node scripts/node-dist.mjs
(cd node && npm run typecheck && npm test && npm run build)
for module in . executor; do
  (cd "$module" && go vet ./... && go build ./...)
done
# Process lifetime and daemon/gateway suites belong to the optional integration gate.
go test ./api ./dsm ./filestation ./jsonlimits ./identity
(cd python && python3 -B -m unittest discover -s tests)
bash scripts/ts-gate.sh
node scripts/check-internal-content.mjs --package node
node scripts/check-internal-content.mjs --package ts
