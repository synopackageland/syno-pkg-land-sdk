#!/usr/bin/env bash
# Imports the packed @synopackageland/sdk as an App's tests do: vitest with
# its default config leaves node_modules to Node's own loader, which refuses
# a .css import in dist. Uses ts/dist (build first) and ts/node_modules, so
# it needs no network.
set -euo pipefail
ts=$(cd "$(dirname "$0")/../ts" && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/sdk-consumer.XXXXXX")
trap 'rm -rf "$work"' EXIT
tgz=$(cd "$ts" && npm pack --ignore-scripts --pack-destination "$work" 2>/dev/null | tail -n 1)
mkdir -p "$work/node_modules/@synopackageland/sdk"
tar xzf "$work/$tgz" -C "$work/node_modules/@synopackageland/sdk" --strip-components 1
for dep in vue @vue @synopackageland/ui @lucide reka-ui; do
	if test -e "$ts/node_modules/$dep"; then ln -s "$ts/node_modules/$dep" "$work/node_modules/$dep"; fi
done
printf '{"type":"module"}\n' >"$work/package.json"
cat >"$work/import.test.js" <<'TEST'
import {expect, test} from 'vitest'
test('the packed SDK loads as an App test imports it', async () => {
	expect((await import('@synopackageland/sdk/vue')).AppShell).toBeDefined()
	expect((await import('@synopackageland/sdk')).isFullPage).toBeDefined()
	const native = await import('@synopackageland/sdk/native')
	expect(new Function(native.ROOT_RUNTIME + ';return createRootAccess;')()).toBeTypeOf('function')
	expect(new Function(native.AUTHORIZATION_RUNTIME + ';return createAuthorizationAccess;')()).toBeTypeOf('function')
})
TEST
(cd "$work" && "$ts/node_modules/.bin/vitest" run --root "$work")
