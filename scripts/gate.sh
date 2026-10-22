#!/usr/bin/env bash
# Local gate. CI has no minutes; this script is the release gate.
# Usage: gate.sh [--fast]
# --fast runs only the segments the changes since SYNOPKGLAND_GATE_BASE
# (default origin/main), committed or not, affect (scripts/gate-plan.sh) and
# checks the executor digests' reproducibility only in the full gate. It is
# for development; a release runs the full gate.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
node --test "$root/scripts/check-internal-content.test.mjs" "$root/scripts/sdk-version.test.mjs"
node "$root/scripts/sdk-version.mjs" --check
node "$root/scripts/check-internal-content.mjs" --repo "$root"
"$root/scripts/scripts.test.sh"

all="go-root go-executor go-conformance rust python node ts"
segments=$all full=yes
case ${1:-} in
'') ;;
--fast)
	full=
	base=${SYNOPKGLAND_GATE_BASE:-origin/main}
	segments=$({
		git -C "$root" diff --name-only "$base...HEAD"
		git -C "$root" diff --name-only HEAD
		git -C "$root" ls-files --others --exclude-standard
	} | "$root/scripts/gate-plan.sh" | tr '\n' ' ')
	skipped=
	for segment in $all; do
		case " $segments " in *" $segment "*) ;; *) skipped="$skipped $segment" ;; esac
	done
	echo "gate --fast: the changes since $base select: ${segments:-nothing}" >&2
	echo "gate --fast: skipped, as no change reaches them (the full gate runs them):${skipped:- nothing}" >&2
	;;
*) echo "usage: gate.sh [--fast]" >&2; exit 2 ;;
esac
want() { case " $segments " in *" $1 "*) ;; *) return 1 ;; esac; }

for pair in .: executor:/executor conformance:/conformance; do
	want="module github.com/synopackageland/syno-pkg-land-sdk${pair#*:}"
	if test "$(head -n 1 "$root/${pair%%:*}/go.mod")" != "$want"; then
		echo "${pair%%:*}/go.mod: want '$want'" >&2
		exit 1
	fi
done
# The root module must be the one at the root: a go.mod in go/ would not resolve from a tag.
if test -e "$root/go"; then
	echo "go/ must not exist; the Go module is at the repository root" >&2
	exit 1
fi
if grep -rnIE --exclude-dir=.git --exclude-dir=node_modules 'github\.com/synopackageland/(sdk|cli|templates|ui)\b' "$root"; then
	echo "old repository paths remain; use syno-pkg-land-*" >&2
	exit 1
fi

# The executor pins the root SDK for the shared dsm.IsAdministrator provider.
# go mod tidy -diff below verifies its exact transitive dependency closure.

# conformance also checks the generated Go client files against spec/.
# rust: crate syno-pkg-land-app (scripts/rust-gate.sh). bridge/golden_test.go and
# runtime_test.go run its examples. On SYNOPKGLAND_DOCKER_SSH both run on Linux
# in Docker (scripts/rust-docker-gate.sh), and the Go tests below skip the examples.
if ! want rust; then
	:
elif test -n "${SYNOPKGLAND_DOCKER_SSH:-}"; then
	"$root/scripts/rust-docker-gate.sh"
else
	"$root/scripts/rust-gate.sh"
	export SYNOPKGLAND_RUST_APP_EXAMPLES="$root/rust/target/debug/examples"
	if test "$(uname -s)" != Linux; then
		"$root/scripts/rust-linux-test.sh"
	fi
fi
# They also run the Node App SDK's hello example and golden App, which import node/dist.
if want go-root || want node; then
	(cd "$root/node" && npm ci --no-audit --no-fund --loglevel=error && npm run build)
fi
for module in . executor conformance; do
	want "go-${module/#./root}" || continue
	(
		cd "$root/$module"
		# The package directories only: the root also holds ts/node_modules and the other modules.
		unformatted=$(gofmt -l $(go list -f '{{.Dir}}' ./...))
		test -z "$unformatted" || { echo "$unformatted" >&2; exit 1; }
		go mod tidy -diff
		go vet ./...
		go test -race -count=1 ./...
	)
done

# bridge App SDKs: standard library only. The Go bridge tests above already
# ran the cross-language runner, which needs python3 and node.
# python: the frames stub matches the schema, the API type-checks, and the
# tests pass here and on DSM's Python 3.8. uv runs the build-time tools.
if want python; then
	"$root/scripts/python-frames.sh" --check
	(cd "$root/python" && uvx --quiet --python 3.12 mypy@1.18.2 --strict --python-version 3.9 synopkgland_app examples/hello/app.py)
	(cd "$root/python" && python3 -B -m unittest discover -s tests)
	"$root/scripts/python38-test.sh"
fi

# node: @synopackageland/app. The test run also checks src/frames.ts and
# src/refusals.ts against spec/.
want node && (
	cd "$root/node"
	npm test
	node ../scripts/check-internal-content.mjs --package .
	node -e '
const pkg = require("./package.json")
if (Object.keys({...pkg.dependencies, ...pkg.peerDependencies, ...pkg.optionalDependencies}).length) {
	console.error("node: @synopackageland/app has no runtime dependencies"); process.exit(1)
}'
	npm pack --dry-run --json --ignore-scripts 2>/dev/null | node -e '
const files = JSON.parse(require("fs").readFileSync(0, "utf8"))[0].files.map(f => f.path)
const bad = files.filter(p => !/^(LICENSE|README\.md|package\.json|dist\/(?:[a-z]+|sdk-version)\.(js|d\.ts))$/.test(p))
const missing = ["LICENSE", "README.md", "dist/index.js", "dist/index.d.ts"].filter(p => !files.includes(p))
if (bad.length || missing.length) { console.error("node: unexpected pack contents", {bad, missing}); process.exit(1) }
console.log("node: " + files.length + " files packed")'
)
# Supported DSM Node.js_v22 runtime compatibility, in Docker.
want node && "$root/scripts/node22-test.sh"

# ts: @synopackageland/sdk (scripts/ts-gate.sh).
want ts && "$root/scripts/ts-gate.sh"

for arch in amd64 arm64 arm; do
	for module in . executor; do
		want "go-${module/#./root}" || continue
		(cd "$root/$module" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch GOARM=7 go vet ./... && CGO_ENABLED=0 GOOS=linux GOARCH=$arch GOARM=7 go build ./...)
	done
done

want go-executor || exit 0
first=$("$root/scripts/executor-digests.sh")
if test -n "$full"; then
	# A cold build: with an empty build cache, nothing of the first build is reused.
	cold=$(mktemp -d)
	second=$(GOCACHE="$cold" "$root/scripts/executor-digests.sh")
	rm -rf "$cold"
	if test "$first" != "$second"; then
		echo "executor builds are not reproducible" >&2
		exit 1
	fi
fi
echo "$first"
