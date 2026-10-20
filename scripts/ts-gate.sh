#!/usr/bin/env bash
# ts: @synopackageland/sdk. The test run also checks src/protocol/v1.ts and i18n/bridge/ against spec/
# and the i18n catalogs against the DSM glossary (test/dsm-glossary.json).
# scripts/gate.sh runs it for the ts segment. On SYNOPKGLAND_DOCKER_SSH it runs
# on Linux in Docker (scripts/ts-docker-gate.sh).
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
if test -n "${SYNOPKGLAND_DOCKER_SSH:-}"; then exec "$root/scripts/ts-docker-gate.sh"; fi
cd "$root/ts"
# npm pack needs node_modules first: prepack builds.
# SYNOPKGLAND_UI_TARBALL: the locked @synopackageland/ui version while it is not on npm.
npm ci --no-audit --no-fund --loglevel=error
if test -n "${SYNOPKGLAND_UI_TARBALL:-}"; then
	npm install --no-save --no-audit --no-fund --loglevel=error "$SYNOPKGLAND_UI_TARBALL"
fi
npm run typecheck
npm test
npm run build
node ../scripts/check-internal-content.mjs --package .
if grep -Eq "from ['\"](vue|@synopackageland/ui|@lucide/vue)['\"/]" dist/index.js dist/native.js; then
	echo "ts: the headless or native entry imports vue or a UI package" >&2
	exit 1
fi
# Apps' tests (vitest) leave node_modules to Node's own loader, which refuses
# a .css import: the packed entries must load there.
"$root/scripts/ts-consumer-test.sh"
# spec/protocol/v1 ships whole: consumers read node_modules/@synopackageland/sdk/spec/protocol/v1.
spec=$(cd "$root" && git ls-files spec/protocol/v1)
npm pack --dry-run --json --ignore-scripts 2>/dev/null | SPEC="$spec" node -e '
const files = JSON.parse(require("fs").readFileSync(0, "utf8"))[0].files.map(f => f.path)
const spec = process.env.SPEC.split("\n")
const allowed = p => /^(LICENSE|README\.md|package\.json)$/.test(p) || /^dist\/(index\.js|vue\.js|native\.js|style\.css|src\/.+\.d\.ts|i18n\/[a-z]+\/[a-zA-Z-]+\.json)$/.test(p) || spec.includes(p)
const bad = files.filter(p => !allowed(p) || /node_modules|\.test\./.test(p))
const missing = ["LICENSE", "README.md", "dist/index.js", "dist/vue.js", "dist/native.js", "dist/style.css", "dist/src/index.d.ts", "dist/src/vue.d.ts", "dist/src/native.d.ts", "dist/i18n/files/en.json", ...spec].filter(p => !files.includes(p))
if (bad.length || missing.length) { console.error("ts: unexpected pack contents", {bad, missing}); process.exit(1) }
console.log("ts: " + files.length + " files packed")'
