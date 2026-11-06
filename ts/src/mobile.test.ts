// @vitest-environment jsdom
import {afterEach, beforeEach, expect, test, vi} from 'vitest'
import {
 saveBlob, saveUrl, openExternal, routeExternalLinks, takeIntake, nativeBridge, createNativeHost,
 nativeLaunchParams, onNativeLaunch, onNativeTransfer,
} from './mobile'
import {
 canInjectNativeBridge, isNativeDownloadURL, createIntakeStore, isNativeMessage, isNativeEvent,
 type NativeBridge, type NativeCapabilities, type NativeEvent,
} from './mobile/shell'

const caps: NativeCapabilities = {download: {base64: true, url: true, maxBase64: 52428800}, openExternal: true, intake: true, camera: 'capture'}
function install(overrides: Partial<NativeCapabilities> = {}) {
 const bridge: NativeBridge = {protocol: 1, shell: {name: 'Package Land', version: '1', platform: 'android'}, caps: {...caps, ...overrides}, post: vi.fn()}
 Object.assign(window, {SynoPkgNative: bridge})
 return bridge
}
function emit(detail: NativeEvent | unknown) {window.dispatchEvent(new CustomEvent('synopkgnative', {detail}))}
let clicks: string[], filenames: string[]
beforeEach(() => {
 clicks = []; filenames = []
 vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function(this: HTMLAnchorElement) {clicks.push(this.href); filenames.push(this.download)})
 vi.spyOn(window, 'open').mockReturnValue(null)
 Object.defineProperty(URL, 'createObjectURL', {configurable: true, value: vi.fn(() => 'blob:browser-download')})
 Object.defineProperty(URL, 'revokeObjectURL', {configurable: true, value: vi.fn()})
})
afterEach(() => {
 vi.unstubAllGlobals()
 window.dispatchEvent(new Event('pagehide'))
 delete (window as Window & {SynoPkgNative?: NativeBridge}).SynoPkgNative
 document.body.innerHTML = ''
 vi.restoreAllMocks(); vi.unstubAllGlobals()
})

test.each(['absent', 'cap-disabled'] as const)('every public action falls back in a browser: %s', async mode => {
 const bridge = mode === 'cap-disabled' ? install({download: false, openExternal: false, intake: false}) : undefined
 const blob = new Blob(['csv'], {type: 'text/csv'})
 expect(await saveBlob(blob, 'report.csv')).toEqual({mode: 'browser'})
 expect(await saveUrl('/file', {filename: 'report.csv'})).toEqual({mode: 'browser'})
 expect(openExternal('https://outside.example/')).toEqual({mode: 'browser'})
 expect(window.open).toHaveBeenCalledWith('https://outside.example/', '_blank', 'noopener')
 expect(clicks).toEqual(['blob:browser-download', `${location.origin}/file`])
 expect(await takeIntake('handle')).toEqual([])
 const listener = vi.fn(), dispose = onNativeLaunch(listener)
 emit({v: 1, type: 'launch', link: '/add'})
 expect(listener).toHaveBeenCalledTimes(bridge ? 1 : 0)
 dispose()
 if(bridge) expect(bridge.post).not.toHaveBeenCalled()
 window.dispatchEvent(new Event('pagehide'))
 expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:browser-download')
})

test('blob bytes and same-origin URL go to the typed native client', async () => {
 const bridge = install()
 expect(await saveBlob(new Blob(['csv'], {type: 'text/csv'}), 'report.csv')).toMatchObject({mode: 'native'})
 expect(bridge.post).toHaveBeenCalledWith(expect.objectContaining({v: 1, type: 'download', filename: 'report.csv', mime: 'text/csv', base64: 'Y3N2'}))
 expect(await saveUrl('/file?q=1', {filename: 'large.bin'})).toMatchObject({mode: 'native'})
 expect(bridge.post).toHaveBeenLastCalledWith(expect.objectContaining({url: `${location.origin}/file?q=1`}))
 expect(clicks).toEqual([])
})

test('oversize blobs, individually missing download caps, foreign URLs and failed dispatch use browser downloads', async () => {
 const bridge = install({download: {base64: true, url: true, maxBase64: 1}})
 await saveBlob(new Blob(['large']), 'large.bin')
 await saveUrl('https://outside.example/file', {filename: 'file'})
 expect(bridge.post).not.toHaveBeenCalled()
 install({download: {base64: false, url: false, maxBase64: 0}})
 await saveBlob(new Blob(['a']), 'a'); await saveUrl('/file', {filename: 'file'})
 const broken = install(); vi.mocked(broken.post).mockImplementation(() => {throw Error('gone')})
 await saveBlob(new Blob(['a']), 'a'); await saveUrl('/file', {filename: 'file'})
 expect(clicks).toHaveLength(6)
})

test('unsafe URLs never reach native or browser actions', async () => {
 const bridge = install()
 for(const url of ['javascript:alert(1)', 'data:text/plain,secret', 'https://user:password@outside.example/']) {
  expect(() => openExternal(url)).toThrow()
  await expect(saveUrl(url, {filename: 'file'})).rejects.toEqual({code: 'invalid_url'})
 }
 expect(bridge.post).not.toHaveBeenCalled(); expect(window.open).not.toHaveBeenCalled(); expect(clicks).toEqual([])
})

test('openExternal posts during user activation, and routes back to browser if post throws', () => {
 const bridge = install()
 expect(openExternal('https://outside.example/')).toEqual({mode: 'native'})
 expect(bridge.post).toHaveBeenCalledWith({v: 1, type: 'openExternal', url: 'https://outside.example/'})
 vi.mocked(bridge.post).mockImplementation(() => {throw Error('gone')})
 expect(openExternal('https://outside.example/')).toEqual({mode: 'browser'})
 vi.stubGlobal('navigator', {userActivation: {isActive: false, hasBeenActive: true}})
 expect(() => openExternal('https://outside.example/')).toThrow()
})

function tap(href: string, attrs: Record<string, string> = {}, modifiers: MouseEventInit = {}) {
 const anchor = document.createElement('a'); anchor.href = href
 for(const [key, value] of Object.entries(attrs)) anchor.setAttribute(key, value)
 document.body.append(anchor)
 const click = new MouseEvent('click', {bubbles: true, cancelable: true, button: 0, ...modifiers})
 anchor.dispatchEvent(click)
 return click.defaultPrevented
}
test.each(['absent', 'cap-disabled'] as const)('link routing leaves ordinary browser behaviour intact: %s', mode => {
 if(mode === 'cap-disabled') install({openExternal: false})
 const dispose = routeExternalLinks()
 expect(tap('https://outside.example/')).toBe(false)
 expect(window.open).not.toHaveBeenCalled()
 dispose()
})
test('link routing handles external and _blank taps, preserves local/download/modifier actions and disposes', () => {
 const bridge = install(), dispose = routeExternalLinks()
 expect(tap('https://outside.example/')).toBe(true)
 expect(tap('/local', {target: '_blank'})).toBe(true)
 expect(tap('/local')).toBe(false)
 expect(tap('/file', {download: ''})).toBe(false)
 expect(tap('https://outside.example/', {}, {ctrlKey: true})).toBe(false)
 expect(bridge.post).toHaveBeenCalledTimes(2)
 vi.mocked(bridge.post).mockImplementation(() => {throw Error('gone')})
 expect(tap('https://outside.example/')).toBe(true)
 expect(window.open).toHaveBeenCalledTimes(1)
 dispose(); expect(tap('https://outside.example/')).toBe(false)
})

test('intake correlates replies and is read once across concurrent calls and replay', async () => {
 const bridge = install(), pending = takeIntake('handle')
 expect(await takeIntake('handle')).toEqual([])
 expect(bridge.post).toHaveBeenCalledTimes(1)
 expect(bridge.post).toHaveBeenCalledWith({v: 1, type: 'intake.take', id: 'handle'})
 emit({v: 1, type: 'reply', id: 'different', ok: true, items: []})
 emit({v: 1, type: 'reply', id: 'handle', ok: true, items: [{kind: 'url', url: 'magnet:?xt=example'}]})
 expect(await pending).toEqual([{kind: 'url', url: 'magnet:?xt=example'}])
 expect(await takeIntake('handle')).toEqual([])
})
test('intake rejects errors and cancellation without reissuing an uncertain read', async () => {
 const bridge = install(), pending = takeIntake('handle')
 emit({v: 1, type: 'reply', id: 'handle', ok: false, error: {code: 'intake_missing'}})
 await expect(pending).rejects.toEqual({code: 'intake_missing'})
 expect(await takeIntake('handle')).toEqual([])
 const controller = new AbortController(), canceled = takeIntake('next', {signal: controller.signal})
 controller.abort(); await expect(canceled).rejects.toEqual({code: 'canceled'})
 expect(await takeIntake('next')).toEqual([])
 vi.mocked(bridge.post).mockImplementation(() => {throw Error('disconnected')})
 await expect(takeIntake('uncertain')).rejects.toEqual({code: 'intake_uncertain'})
 expect(await takeIntake('uncertain')).toEqual([])
})

test('launch and transfer events validate, map into ctx.onLaunch params and unsubscribe', () => {
 install()
 const launch = vi.fn(), transfer = vi.fn(), offLaunch = onNativeLaunch(launch), offTransfer = onNativeTransfer(transfer)
 emit({v: 1, type: 'launch', link: '/add', intake: 'handle'})
 expect(launch).toHaveBeenCalledWith({route: '/add', link: '/add', intake: 'handle'})
 emit({v: 2, type: 'launch', link: '/wrong'})
 emit({v: 1, type: 'launch', link: '//outside.example/'})
 expect(launch).toHaveBeenCalledTimes(1)
 emit({v: 1, type: 'transfer', id: 'download-1', state: 'downloading', progress: 0.5})
 expect(transfer).toHaveBeenCalledTimes(1)
 offLaunch(); offTransfer()
 emit({v: 1, type: 'launch', link: '/add'})
 expect(launch).toHaveBeenCalledTimes(1)
 expect(nativeLaunchParams({link: '/cold', intake: 'cold-id'})).toEqual({link: '/cold', route: '/cold', intake: 'cold-id'})
 expect(nativeLaunchParams({link: 'javascript:alert(1)', intake: '../path'})).toEqual({})
})

test('native host preserves the HostAdapter shape and falls back without a bridge', () => {
 const host = createNativeHost({appLocale: 'zh-TW'})
 expect(host.platform()).toMatchObject({host: 'standalone-web', appLocale: 'zh-TW'})
 install(); expect(host.platform()).toMatchObject({host: 'native-shell', appLocale: 'zh-TW'})
 expect(host.openURL('https://outside.example/')).toEqual({status: 'requested', windowHandleAvailable: false})
 host.dispose()
})

test('SDK detection rejects unsupported protocols, malformed capabilities and all subframes', () => {
 const bridge = install()
 expect(nativeBridge()).toBe(bridge)
 Object.assign(window, {SynoPkgNative: {...bridge, protocol: 2}})
 expect(nativeBridge()).toBeUndefined()
 Object.assign(window, {SynoPkgNative: {...bridge, caps: {intake: true}}})
 expect(nativeBridge()).toBeUndefined()
 install()
 vi.stubGlobal('window', {top: {}, location, SynoPkgNative: bridge})
 expect(nativeBridge()).toBeUndefined()
})

test('shell policy is NAS-origin and main-frame only; URL downloads cannot cross the origin', () => {
 const nas = 'https://nas.example:5001'
 expect(canInjectNativeBridge(nas, `${nas}/webman/3rdparty/App/app/entry.html`, true)).toBe(true)
 for(const frame of ['https://outside.example/', 'https://nas.example/', 'http://nas.example:5001/', 'https://name:pass@nas.example:5001/', 'data:text/html,hello', 'about:blank']) {
  expect(canInjectNativeBridge(nas, frame, true)).toBe(false)
  expect(isNativeDownloadURL(nas, frame)).toBe(false)
 }
 expect(canInjectNativeBridge(nas, `${nas}/`, false)).toBe(false)
 expect(canInjectNativeBridge(`${nas}/path`, `${nas}/`, true)).toBe(false)
})

test('shell intake store deletes on read, prevents handle reuse and isolates session state', () => {
 const store = createIntakeStore(), items = [{kind: 'text' as const, text: 'shared'}]
 store.put('handle', items); items[0].text = 'mutated'
 expect(store.take('handle')).toEqual([{kind: 'text', text: 'shared'}])
 expect(store.take('handle')).toEqual([])
 expect(() => store.put('handle', items)).toThrow()
 store.put('next', items); store.clear(); expect(store.take('next')).toEqual([])
 expect(() => store.put('bad', [{kind: 'file', filename: '../path', mime: '', base64: '', size: 0}])).toThrow()
})

test('wire validators refuse mixed download modes, unknown keys, paths, malformed base64 and invalid progress', () => {
 const download = {v: 1, type: 'download', id: 'd', filename: 'file', mime: '', base64: 'YQ=='}
 expect(isNativeMessage(download)).toBe(true)
 expect(isNativeMessage({...download, url: 'https://nas.example/'})).toBe(false)
 expect(isNativeMessage({...download, base64: 'not base64'})).toBe(false)
 expect(isNativeEvent({v: 1, type: 'transfer', id: 'd', state: 'downloading', progress: 2})).toBe(false)
 expect(isNativeMessage({v: 1, type: 'openExternal', url: 'https://outside.example/', token: 'forbidden'})).toBe(false)
})


const filenameCases: [string, string][] = [
 ['folder/report.csv', 'folder report.csv'],
 ['folder\\report.csv', 'folder report.csv'],
 ...Array.from({length: 32}, (_, i): [string, string] => [`report${String.fromCharCode(i)}draft.csv`, 'report draft.csv']),
 ['report\u007fdraft.csv', 'report draft.csv'],
 ...Array.from({length: 32}, (_, i): [string, string] => [`report${String.fromCharCode(128 + i)}draft.csv`, 'report draft.csv']),
 ['  .. report  draft ... csv . ', 'report draft.csv'],
 ['report . . csv', 'report.csv'],
 ['', 'download'], [' .. . /\\\t\n ', 'download'],
 ['報表 設計稿.csv', '報表 設計稿.csv'],
 ['筆記😀.txt', '筆記😀.txt'],
 ['a'.repeat(300) + '.csv', 'a'.repeat(251) + '.csv'],
 ['報'.repeat(100) + '.csv', '報'.repeat(83) + '.csv'],
 ['😀'.repeat(100) + '.txt', '😀'.repeat(62) + '.txt'],
 ['a'.repeat(300), 'a'.repeat(255)],
 ['a'.repeat(250) + '.draft.csv', 'a'.repeat(250) + '.csv'],
 ['report.' + 'x'.repeat(300), 'report.' + 'x'.repeat(248)],
]
for(const mode of ['browser', 'native', 'dispatch-failure', 'oversize'] as const) {
 test.each(filenameCases)(`download filenames are safe basenames in ${mode}: %j`, async (input, expected) => {
  const bridge = mode === 'browser' ? undefined : install(mode === 'oversize' ? {download: {base64: true, url: true, maxBase64: 0}} : {})
  if(mode === 'dispatch-failure') vi.mocked(bridge!.post).mockImplementation(() => {throw Error('gone')})
  await saveBlob(new Blob(['csv']), input)
  await saveUrl('/file', {filename: input})
  for(const [message] of vi.mocked(bridge?.post ?? vi.fn()).mock.calls) {
   expect(message).toMatchObject({filename: expected})
   expect(isNativeMessage(message)).toBe(true)
  }
  const browserNames = mode === 'native' ? [] : mode === 'oversize' ? [expected] : [expected, expected]
  expect(filenames).toEqual(browserNames)
  expect(expected).not.toMatch(/[/\\\u0000-\u001f\u007f-\u009f]/)
  expect(new TextEncoder().encode(expected).length).toBeLessThanOrEqual(255)
 })
}

test('invalid MIME metadata still cannot reach either download transport', async () => {
 const bridge = install()
 for(const mime of ['text/plain\r\nInjected: yes', 'a'.repeat(256)]) {
  await expect(saveUrl('/file', {filename: '../report.csv', mime})).rejects.toEqual({code: 'invalid_filename'})
 }
 expect(bridge.post).not.toHaveBeenCalled(); expect(clicks).toEqual([])
})
