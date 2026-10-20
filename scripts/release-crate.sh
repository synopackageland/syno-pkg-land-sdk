#!/usr/bin/env bash
# SDK release step, after the matching npm packages. Dry-run by default.
# Run inside a Linux container on the release builder; never run Rust on macOS.
set -euo pipefail
mode=${1:---dry-run}
if test "$#" -gt 1 || { test "$mode" != --dry-run && test "$mode" != --publish; }; then
	echo 'usage: scripts/release-crate.sh [--dry-run|--publish]' >&2
	exit 2
fi
if test "$(uname -s)" != Linux || ! test -f /.dockerenv; then
	echo 'Run this step inside a Linux container on the release builder.' >&2
	exit 1
fi
: "${SPL_INTERNAL_DENYLIST:?Set SPL_INTERNAL_DENYLIST to the external policy file}"
test -r "$SPL_INTERNAL_DENYLIST"
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
manifest=rust/app/Cargo.toml
metadata=$(cargo metadata --manifest-path "$manifest" --no-deps --format-version 1 --locked)
version=$(printf '%s' "$metadata" | node -e '
const fs = require("node:fs");
const meta = JSON.parse(fs.readFileSync(0, "utf8"));
const pkg = meta.packages.find(p => p.name === "syno-pkg-land-app");
for (const path of ["ts/package.json", "node/package.json"]) {
  if (JSON.parse(fs.readFileSync(path, "utf8")).version !== pkg.version)
    throw new Error("Crate and npm SDK versions must match");
}
process.stdout.write(pkg.version);
')
target=$(printf '%s' "$metadata" | node -e 'process.stdout.write(JSON.parse(require("node:fs").readFileSync(0, "utf8")).target_directory)')

if test "$mode" = --publish; then
	: "${CARGO_REGISTRY_TOKEN:?Pass the registry token through the container environment}"
	# Keep failing Git commands out of test substitutions: two empty results
	# must never let an archive without Git metadata pass the release-tag check.
	git rev-parse --is-inside-work-tree >/dev/null
	status=$(git status --porcelain)
	test -z "$status" || { echo 'Publishing requires a clean SDK checkout.' >&2; exit 1; }
	head=$(git rev-parse HEAD)
	tag=$(git rev-parse "v$version^{commit}")
	test "$head" = "$tag" || {
		echo 'Publishing requires the matching SDK release tag at HEAD.' >&2; exit 1;
	}
	node --input-type=module - "$version" <<'JS'
const version = process.argv[2];
for (const name of ["@synopackageland/sdk", "@synopackageland/app"]) {
  const response = await fetch(`https://registry.npmjs.org/${encodeURIComponent(name)}/${version}`, {
    signal: AbortSignal.timeout(30000),
  });
  if (!response.ok) throw new Error(`Publish ${name}@${version} on npm first`);
  const pkg = await response.json();
  if (pkg.name !== name || pkg.version !== version || !pkg.dist?.integrity)
    throw new Error(`Unexpected npm release metadata for ${name}`);
}
JS
fi

cargo package --manifest-path "$manifest" --locked --list
cargo package --manifest-path "$manifest" --locked
node scripts/check-internal-content.mjs --tarball "$target/package/syno-pkg-land-app-$version.crate"
args=(publish --manifest-path "$manifest" --locked --registry crates-io)
if test "$mode" = --dry-run; then args+=(--dry-run); fi
cargo "${args[@]}"
