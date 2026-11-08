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
	const entry = await import('@synopackageland/sdk/entry')
	expect(entry.bootEntryPage).toBeTypeOf('function')
	expect(entry.createAppContext).toBeTypeOf('function')
	expect(entry.createSignInCache).toBeTypeOf('function')
	expect((await import('@synopackageland/sdk/mobile')).saveBlob).toBeTypeOf('function')
	const shell = await import('@synopackageland/sdk/mobile/shell')
	expect(shell.NATIVE_PROTOCOL).toBe(1)
	expect(shell.isNativeMessage({v: 1, type: 'ready', fcp: 1})).toBe(true)
	const native = await import('@synopackageland/sdk/native')
	expect(new Function(native.ROOT_RUNTIME + ';return createRootAccess;')()).toBeTypeOf('function')
	expect(new Function(native.AUTHORIZATION_RUNTIME + ';return createAuthorizationAccess;')()).toBeTypeOf('function')
})
test('the packed application launcher exposes typed targets and dispatches through its desktop capability', async () => {
 const {openApp, createDsmAppLauncher, dsmAppLaunchURL} = await import('@synopackageland/sdk')
 const target = {appId: 'SYNO.SDS.MailPlusServer.Instance', params: {route: '/a/b?x=1&y=2'}}
 expect(await openApp(target, {desktop: undefined})).toEqual({status: 'unsupported'})
 const calls = []
 const launcher = createDsmAppLauncher({desktop: {AppLaunch: (...args) => calls.push(args)}})
 expect(await launcher.openApp(target)).toEqual({status: 'requested'})
 expect(calls).toEqual([[target.appId, target.params]])
 expect(new URL(dsmAppLaunchURL('https://dsm.example:5001', target)).searchParams.get('launchApp')).toBe(target.appId)
})
TEST
(cd "$work" && "$ts/node_modules/.bin/vitest" run --root "$work")

cat >"$work/consumer.ts" <<'TYPES'
import {bootEntryPage, createAppContext, createSignInCache} from '@synopackageland/sdk/entry'
import {saveBlob, createNativeHost} from '@synopackageland/sdk/mobile'
import {NATIVE_PROTOCOL, type NativeBridge, type NativeMessage, type NativeEvent} from '@synopackageland/sdk/mobile/shell'
import {openApp, createDsmAppLauncher, dsmAppLaunchURL, type DsmAppTarget, type DsmAppLaunchResult, type DsmAppLauncher} from '@synopackageland/sdk'
const target: DsmAppTarget = {appId: 'SYNO.SDS.MailPlusServer.Instance'}
const launcher: DsmAppLauncher = createDsmAppLauncher({desktop: undefined})
const result: Promise<DsmAppLaunchResult> = launcher.openApp(target)
void result
void openApp(target, {desktop: undefined})
void dsmAppLaunchURL('https://dsm.example:5001', target)
const message: NativeMessage = {v: NATIVE_PROTOCOL, type: 'intake.take', id: 'handle'}
const launch: NativeEvent = {v: NATIVE_PROTOCOL, type: 'launch', link: '/add', intake: 'handle'}
declare const bridge: NativeBridge
bridge.post(message)
void launch
void saveBlob(new Blob(['csv']), 'report.csv')
void createNativeHost()
void bootEntryPage
void createAppContext
void createSignInCache
TYPES
(cd "$work" && "$ts/node_modules/.bin/tsc" --noEmit --module NodeNext --moduleResolution NodeNext --target ES2022 --skipLibCheck consumer.ts)
