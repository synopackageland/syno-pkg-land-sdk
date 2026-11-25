// @vitest-environment jsdom
// @vitest-environment-options {"url":"https://localhost/"}
import {afterEach, beforeEach, expect, test, vi} from 'vitest'
import type {Context} from '../context'
import {bootEntryPage, createAppContext, entryLaunchParams, ENTRY_STATUS_RETRY_DELAYS_MS, PORTAL_FALLBACK_PROBE_MS} from '../entry'
import {readDsmLanguage} from './language'

// The DSM display-language read has its own tests (language.test.ts); here
// DSM does not answer it unless a test says so.
vi.mock('./language', () => ({readDsmLanguage: vi.fn(async () => undefined)}))

const entryURL = 'https://localhost/webman/3rdparty/ExampleApp/app/entry.html?launch=123&link=%2Fadd&intake=item'
const account = {user: 'alice', admin: false, lang: 'cht', locale: 'zh-TW'}
const json = (data: unknown) => new Response(JSON.stringify({success: true, data}))
const root = () => document.querySelector<HTMLElement>('.dsmapp-root')!
const statusFetch = () => vi.fn<typeof globalThis.fetch>(async () => json(account))
const options = () => ({packageId: 'ExampleApp', build: 'build-a', token: 'sign-in', pageURL: entryURL, root: root(), mount: vi.fn<(root: HTMLElement, ctx: Context) => unknown>(), navigate: vi.fn<(url: string) => void>()})

beforeEach(() => {
 localStorage.clear()
 document.head.innerHTML = ''
 document.body.innerHTML = '<div class="dsmapp-root"></div>'
 history.replaceState(null, '', entryURL)
})
afterEach(() => {vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks()})

test('cache miss calls status once, mounts without DSM navigation, and no portal keeps the entry URL', async () => {
 const request = statusFetch(), opts = options(), pathname = location.pathname
 const ctx = await bootEntryPage({...opts, fetch: request})
 expect(opts.navigate).not.toHaveBeenCalled()
 expect(opts.mount).toHaveBeenCalledOnce()
 expect(request).toHaveBeenCalledTimes(1)
 expect(request.mock.calls[0]![0]).toBe('https://localhost/webman/3rdparty/ExampleApp/cgi/api.cgi')
 expect(JSON.parse(String(request.mock.calls[0]![1]?.body))).toEqual({method: 'app.status', params: {}})
 expect(ctx).toMatchObject({locale: 'zh-TW', lang: 'cht', fullPage: true, user: {name: 'alice', isAdmin: false}, launchParams: {link: '/add', route: '/add', intake: 'item'}})
 expect(ctx?.files?.token?.()).toBe('sign-in')
 expect(ctx?.portal).toBeUndefined()
 expect(location.pathname).toBe(pathname)
 ctx!.host.dispose()
 // The same sign-in needs no second identity read.
 const cached = await bootEntryPage({...opts, fetch: request})
 expect(request).toHaveBeenCalledTimes(1)
 cached!.host.dispose()
})

test.each([['cht', {dsmLanguage: 'cht'}], ['', {dsmLanguage: ''}], [undefined, {}]])('DSM display language %j reaches app.status as its hint', async (language, params) => {
 vi.mocked(readDsmLanguage).mockResolvedValueOnce(language)
 const request = statusFetch()
 const ctx = await bootEntryPage({...options(), fetch: request, requestTimeout: 9000})
 expect(readDsmLanguage).toHaveBeenLastCalledWith(request, 'https://localhost/webapi/entry.cgi', 'sign-in', 9000)
 expect(JSON.parse(String(request.mock.calls[0]![1]?.body))).toEqual({method: 'app.status', params})
 ctx!.host.dispose()
})

test('older status without language mounts using browser language', async () => {
 vi.stubGlobal('navigator', {language: 'ja-JP'})
 const ctx = await bootEntryPage({...options(), fetch: vi.fn(async () => json({user: 'alice', admin: false}))})
 expect(ctx?.locale).toBe('ja')
 ctx!.host.dispose()
})

test('missing identity and definite API error navigate to DSM preserving launch intent', async () => {
 for(const response of [json({user: '', admin: false}), new Response(JSON.stringify({success: false, error: {code: 'session_required'}}))]) {
  const opts = options()
  expect(await bootEntryPage({...opts, fetch: vi.fn(async () => response)})).toBeUndefined()
  expect(opts.mount).not.toHaveBeenCalled()
  expect(opts.navigate).toHaveBeenCalledOnce()
  const url = new URL(opts.navigate.mock.calls[0]![0])
  expect(url.searchParams.get('launchApp')).toBe('SYNO.SDS.ExampleApp.Instance')
  expect(url.searchParams.get('launchParam')).toContain('link=%2Fadd')
  expect(url.searchParams.get('launchParam')).toContain('intake=item')
 }
})

test('a missing token goes to DSM without calling app.status', async () => {
 const opts = {...options(), token: undefined}
 const request = vi.fn<typeof globalThis.fetch>(async () => new Response(JSON.stringify({success: false})))
 await bootEntryPage({...opts, fetch: request})
 expect(opts.navigate).toHaveBeenCalledOnce()
 expect(opts.mount).not.toHaveBeenCalled()
 expect(request).toHaveBeenCalledTimes(1)
 expect(request.mock.calls[0]![0]).toBe('https://localhost/webman/login.cgi?enable_syno_token=yes')
})
test('valid portal rewrites after a probe; a missing alias retains the working entry URL', async () => {
 for(const status of [404, 200]) {
  localStorage.clear()
  history.replaceState(null, '', entryURL)
  const opts = options()
  const request = vi.fn<typeof globalThis.fetch>(async (url, init) => {
   if(init?.method === 'POST' && String(init.body).includes('app.status')) return json(account)
   return new Response('', {status: String(url) === 'https://localhost/example/' ? status : 404})
  })
  const ctx = await bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}, rewriteToAlias: true})
  expect(location.pathname).toBe(status === 200 ? '/example/' : '/webman/3rdparty/ExampleApp/app/entry.html')
  if(status === 200) expect(location.search).toBe('?link=%2Fadd&intake=item')
  expect(opts.mount).toHaveBeenCalledOnce()
  expect(opts.navigate).not.toHaveBeenCalled()
  ctx!.host.dispose()
 }
})

const buildA = 'a'.repeat(64), buildB = 'b'.repeat(64)
const pointer = (build: unknown) => new Response(JSON.stringify({v: 1, build}))

test('uiUpdate compares the exact CLI hash and handles missing metadata, refusals and offline', async () => {
 let html = `<meta name="synopkgland-build" content="${buildA}">`
 const request = vi.fn<typeof globalThis.fetch>(async () => new Response(html))
 const ctx = createAppContext({packageId: 'ExampleApp', build: buildA, token: 'token', pageURL: entryURL, fetch: request})
 expect(await ctx.uiUpdate!.check()).toBe(false)
 html = `<meta content="${buildB}" name="synopkgland-build">`
 expect(await ctx.uiUpdate!.check()).toBe(true)
 html = '<script src="app.js?v=another-build"></script>'
 expect(await ctx.uiUpdate!.check()).toBe(false)
 request.mockRejectedValue(new Error('offline'))
 expect(await ctx.uiUpdate!.check()).toBe(false)
 expect(String(request.mock.calls[0]![0])).toContain('/app/entry.json?launch=')
 expect(String(request.mock.calls[1]![0])).toContain('/app/entry.html?launch=')
 expect(request.mock.calls[0]![1]).toMatchObject({cache: 'no-store'})
 ctx.host.dispose()
})

test('uiUpdate reads the build from the fixed entry page pointer first', async () => {
 let build = buildA
 const request = vi.fn<typeof globalThis.fetch>(async url => String(url).includes('entry.json')
  ? pointer(build) : new Response(`<meta name="synopkgland-build" content="${buildB}">`))
 const ctx = createAppContext({packageId: 'ExampleApp', build: buildA, token: 'token', pageURL: entryURL, fetch: request})
 expect(await ctx.uiUpdate!.check()).toBe(false)
 build = buildB
 expect(await ctx.uiUpdate!.check()).toBe(true)
 expect(request.mock.calls.every(([url]) => String(url).includes('entry.json'))).toBe(true)
 ctx.host.dispose()
})

// The DSM desktop, a portal alias and the entry page itself read the App's
// entry.json on their own prefix; an App gateway on another prefix may answer.
test.each([
 ['DSM desktop', 'https://nas.example:5001/?launchApp=SYNO.SDS.ExampleApp.Instance', 'https://nas.example:5001/webman/3rdparty/ExampleApp/app/entry.json'],
 ['portal alias', 'https://nas.example:5001/example/#/overview', 'https://nas.example:5001/example/webman/3rdparty/ExampleApp/app/entry.json'],
 ['entry page', 'https://nas.example:5001/webman/3rdparty/ExampleApp/app/launch.html', 'https://nas.example:5001/webman/3rdparty/ExampleApp/app/entry.json'],
])('uiUpdate on the %s reads that prefix\'s entry.json', async (_mode, pageURL, expected) => {
 const request = vi.fn<typeof globalThis.fetch>(async () => pointer(buildB))
 const ctx = createAppContext({packageId: 'ExampleApp', build: buildA, token: 'token', pageURL, fetch: request})
 expect(await ctx.uiUpdate!.check()).toBe(true)
 const url = new URL(String(request.mock.calls[0]![0]))
 expect(url.origin + url.pathname).toBe(expected)
 ctx.host.dispose()
})

test('uiUpdate is unknown, not stale, for anything but two CLI builds', async () => {
 const answers: Array<[string, () => Response]> = [
  ['a gateway sign-in refusal', () => new Response(JSON.stringify({error: 'dsm_sign_in_required'}), {status: 401})],
  ['a gateway JSON answer', () => new Response(JSON.stringify({success: false, error: {code: 119}}))],
  ['a single-page app', () => new Response('<!doctype html><div id="app"></div>')],
  ['a pointer of another version', () => new Response(JSON.stringify({v: 2, build: buildB}))],
  ['a pointer with a short build', () => pointer('6f49e3f0b24f')],
  ['a missing package', () => new Response('Not found', {status: 404})],
 ]
 for(const [, answer] of answers) {
  const ctx = createAppContext({packageId: 'ExampleApp', build: buildA, token: 'token', pageURL: entryURL, fetch: vi.fn(async () => answer())})
  expect(await ctx.uiUpdate!.check()).toBe(false)
  ctx.host.dispose()
 }
 // A page whose build is not a CLI hash, as a window another tool rendered, never asks.
 const request = vi.fn<typeof globalThis.fetch>(async () => pointer(buildB))
 const ctx = createAppContext({packageId: 'ExampleApp', build: '6f49e3f0b24f', token: 'token', pageURL: entryURL, fetch: request})
 expect(await ctx.uiUpdate!.check()).toBe(false)
 expect(request).not.toHaveBeenCalled()
 ctx.host.dispose()
})

test('a hung portal probe does not hold up mount or navigate away', async () => {
 const opts = options()
 const ctx = await bootEntryPage({...opts, portal: {defaultAlias: 'example'}, rewriteToAlias: true, fetch: async (_url, init) => {
  if(init?.method === 'GET') return new Promise<Response>(() => {})
  return json(account)
 }})
 expect(opts.mount).toHaveBeenCalledOnce()
 expect(opts.navigate).not.toHaveBeenCalled()
 expect(location.pathname).toBe('/webman/3rdparty/ExampleApp/app/entry.html')
 ctx!.host.dispose()
})

test('native warm launches compose with the window and all subscriptions dispose', () => {
 vi.stubGlobal('SynoPkgNative', {protocol: 1, shell: {name: 'Test shell', version: '1', platform: 'android'},
  caps: {download: false, openExternal: false, intake: true, camera: false}, post: vi.fn()})
 const stopWindow = vi.fn(), dsm = vi.fn(() => stopWindow), listener = vi.fn()
 const ctx = createAppContext({packageId: 'ExampleApp', build: 'a', pageURL: entryURL, onLaunch: dsm})
 expect(ctx.host.platform().host).toBe('native-shell')
 const dispose = ctx.onLaunch!(listener)
 window.dispatchEvent(new CustomEvent('synopkgnative', {detail: {v: 1, type: 'launch', link: '/next', intake: 'next'}}))
 expect(listener).toHaveBeenCalledWith({link: '/next', route: '/next', intake: 'next'})
 expect(dsm).toHaveBeenCalledWith(listener)
 dispose(); dispose()
 expect(stopWindow).toHaveBeenCalledOnce()
 window.dispatchEvent(new CustomEvent('synopkgnative', {detail: {v: 1, type: 'launch', link: '/later'}}))
 expect(listener).toHaveBeenCalledOnce()
 ctx.onLaunch!(listener)
 ctx.host.dispose()
 expect(stopWindow).toHaveBeenCalledTimes(2)
})

test('portal rewrite preserves the route the App opened while the probe was pending', async () => {
 let answer!: (value: Response) => void
 const ctx = await bootEntryPage({...options(), portal: {defaultAlias: 'example'}, rewriteToAlias: true, fetch: async (_url, init) => {
  if(init?.method === 'GET') return new Promise<Response>(resolve => {answer = resolve})
  return json(account)
 }})
 location.hash = '/opened'
 answer(new Response(''))
 await vi.waitFor(() => expect(location.pathname).toBe('/example/'))
 expect(location.hash).toBe('#/opened')
 ctx!.host.dispose()
})

test('native entry reports an observed FCP without changing the mounted page', async () => {
 const post = vi.fn()
 vi.stubGlobal('SynoPkgNative', {protocol: 1, shell: {name: 'Test shell', version: '1', platform: 'android'},
  caps: {download: false, openExternal: false, intake: true, camera: false}, post})
 vi.stubGlobal('performance', {now: () => 100, getEntriesByName: () => [{name: 'first-contentful-paint', startTime: 75}]})
 const opts = options()
 const ctx = await bootEntryPage({...opts, fetch: statusFetch()})
 expect(opts.mount).toHaveBeenCalledOnce()
 expect(post).toHaveBeenCalledWith({v: 1, type: 'ready', fcp: 75})
 ctx!.host.dispose()
})

test('shared ctx keeps supplied window adapters and translates App catalogs', async () => {
 const request = statusFetch(), reopen = vi.fn(() => () => {}), launch = vi.fn(() => () => {})
 const ctx = createAppContext({packageId: 'ExampleApp', build: 'a', pageURL: entryURL, token: 'token', fetch: request,
  locale: 'zh-TW', fullPage: false, reopen, onLaunch: launch, messages: {en: {hello: 'Hello {name}'}, 'zh-TW': {hello: '你好 {name}'}}})
 expect(ctx.t!('hello', {name: 'Alice'})).toBe('你好 Alice')
 expect(ctx.t!('missing')).toBe('missing')
 expect(ctx.fullPage).toBe(false)
 const listener = vi.fn(), dispose = ctx.onLaunch!(listener)
 expect(launch).toHaveBeenCalledWith(listener)
 dispose()
 await ctx.call('read')
 expect(request.mock.calls[0]![1]?.headers).toMatchObject({'X-SYNO-TOKEN': 'token'})
 ctx.host.dispose()
})

test('mount errors propagate and launch params reject reserved, invalid and oversized inputs', async () => {
 const opts = options()
 await expect(bootEntryPage({...opts, fetch: statusFetch(), mount() {throw new Error('mount failed')}})).rejects.toThrow('mount failed')
 const parsed = entryLaunchParams(`?className=x&launch=1&link=%2F%2Fevil&bad=%00&long=${'a'.repeat(2049)}&valid=yes`)
 expect(parsed).toMatchObject({valid: 'yes'})
 expect(parsed).not.toHaveProperty('className')
 expect(parsed).not.toHaveProperty('launch')
 expect(parsed).not.toHaveProperty('bad')
 expect(parsed).not.toHaveProperty('long')
 expect(parsed).not.toHaveProperty('route')
})


test('preloads the declared bundle before token or identity resolves, mounting only after both succeed', async () => {
 const opts = {...options(), token: undefined, appBundle: './app.js?v=build-a'}
 let token!: (response: Response) => void, identity!: (response: Response) => void
 const request = vi.fn<typeof globalThis.fetch>((url) => {
  const preload = document.querySelector<HTMLLinkElement>('link[rel=modulepreload]')
  expect(preload?.href).toBe('https://localhost/webman/3rdparty/ExampleApp/app/app.js?v=build-a')
  expect(preload?.crossOrigin).toBe('anonymous')
  return new Promise(resolve => {if(String(url).includes('login.cgi')) token = resolve; else identity = resolve})
 })
 const pending = bootEntryPage({...opts, fetch: request})
 expect(request).toHaveBeenCalledTimes(1)
 expect(opts.mount).not.toHaveBeenCalled()
 expect(document.querySelector('script[src]')).toBeNull()
 token(new Response(JSON.stringify({success: true, SynoToken: 'token'})))
 await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(2))
 expect(opts.mount).not.toHaveBeenCalled()
 identity(json(account))
 const ctx = await pending
 expect(opts.mount).toHaveBeenCalledOnce()
 ctx!.host.dispose()
})

test.each(['token', 'identity'] as const)('a failed %s never mounts a preloaded bundle', async failure => {
 const opts = {...options(), token: undefined, appBundle: './app.js?v=build-a'}
 const request = vi.fn<typeof globalThis.fetch>(async url => String(url).includes('login.cgi') && failure !== 'token'
  ? new Response(JSON.stringify({success: true, SynoToken: 'token'})) : json({user: '', admin: false}))
 expect(await bootEntryPage({...opts, fetch: request})).toBeUndefined()
 expect(document.querySelector('link[rel=modulepreload]')).not.toBeNull()
 expect(opts.mount).not.toHaveBeenCalled()
 expect(opts.navigate).toHaveBeenCalledOnce()
})

test('preloads a classic App script and first-screen modules before the token, without executing them', async () => {
 const opts = {...options(), token: undefined, appScript: './app.js?v=build-a', preload: ['./chunks/main-1a2b.js', './chunks/vendor-3c4d.js']}
 let token!: (response: Response) => void
 const request = vi.fn<typeof globalThis.fetch>(url => {
  if(String(url).includes('login.cgi')) return new Promise(resolve => {token = resolve})
  return Promise.resolve(json(account))
 })
 const pending = bootEntryPage({...opts, fetch: request})
 const script = document.querySelector<HTMLLinkElement>('link[rel=preload]')
 expect(script?.as).toBe('script')
 expect(script?.href).toBe('https://localhost/webman/3rdparty/ExampleApp/app/app.js?v=build-a')
 expect(script?.crossOrigin).toBeNull()
 expect(Array.from(document.querySelectorAll<HTMLLinkElement>('link[rel=modulepreload]')).map(link => [link.href, link.crossOrigin])).toEqual([
  ['https://localhost/webman/3rdparty/ExampleApp/app/chunks/main-1a2b.js', 'anonymous'],
  ['https://localhost/webman/3rdparty/ExampleApp/app/chunks/vendor-3c4d.js', 'anonymous'],
 ])
 expect(document.querySelector('script[src]')).toBeNull()
 expect(opts.mount).not.toHaveBeenCalled()
 token(new Response(JSON.stringify({success: true, SynoToken: 'token'})))
 const ctx = await pending
 expect(opts.mount).toHaveBeenCalledOnce()
 ctx!.host.dispose()
})

test('one unsafe first-screen URL refuses every preload before identity', async () => {
 const opts = options(), request = statusFetch()
 await expect(bootEntryPage({...opts, appScript: './app.js?v=build-a', preload: ['./chunks/main.js', 'https://outside.example/x.js'], fetch: request})).rejects.toThrow('Invalid App bundle URL')
 expect(document.querySelector('link[rel=preload], link[rel=modulepreload]')).toBeNull()
 expect(request).not.toHaveBeenCalled()
 expect(opts.mount).not.toHaveBeenCalled()
})

test.each(['https://outside.example/app.js', '//outside.example/app.js', 'javascript:alert(1)', 'data:text/javascript,alert(1)', 'https://user:pass@localhost/app.js'])('refuses an unsafe bundle URL before preload or identity: %s', async appBundle => {
 const opts = options(), request = statusFetch()
 await expect(bootEntryPage({...opts, appBundle, fetch: request})).rejects.toThrow('Invalid App bundle URL')
 expect(document.querySelector('link[rel=modulepreload]')).toBeNull()
 expect(request).not.toHaveBeenCalled()
 expect(opts.mount).not.toHaveBeenCalled()
})

/** DSM's launchParam on a sign-in URL, read the way DSM and the entry read it. */
const launchOf = (url: URL): Record<string, string> => ({...entryLaunchParams(`?launchParam=${encodeURIComponent(url.searchParams.get('launchParam') ?? '')}`)})
const reply = (code: string) => new Response(JSON.stringify({success: false, error: {code}}))
const statusCalls = (request: ReturnType<typeof vi.fn>) => request.mock.calls.filter(([, init]) => String((init as RequestInit | undefined)?.body ?? '').includes('"app.status"')).length

test.each([
 ['busy', () => reply('busy')],
 ['request_timeout', () => reply('request_timeout')],
 ['a network error', () => Promise.reject(new TypeError('Failed to fetch'))],
 ['an unreadable reply', () => new Response('<html>502</html>', {status: 502})],
])('app.status %s retries with backoff and mounts without leaving the entry', async (_name, failure) => {
 vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
 const opts = options()
 let attempts = 0
 const request = vi.fn<typeof globalThis.fetch>(async () => ++attempts <= 2 ? failure() : json(account))
 const pending = bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}})
 await vi.advanceTimersByTimeAsync(0)
 expect(attempts).toBe(1)
 await vi.advanceTimersByTimeAsync(ENTRY_STATUS_RETRY_DELAYS_MS[0]!)
 expect(attempts).toBe(2)
 await vi.advanceTimersByTimeAsync(ENTRY_STATUS_RETRY_DELAYS_MS[1]!)
 const ctx = await pending
 expect(statusCalls(request)).toBe(3)
 expect(opts.mount).toHaveBeenCalledOnce()
 expect(opts.navigate).not.toHaveBeenCalled()
 expect(ctx?.user).toEqual({name: 'alice', isAdmin: false})
 ctx!.host.dispose()
})

test('the retries stop after a fixed number of attempts, not a clock, then fall back', async () => {
 vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
 const opts = options()
 const request = vi.fn<typeof globalThis.fetch>(async () => reply('busy'))
 const pending = bootEntryPage({...opts, fetch: request})
 await vi.advanceTimersByTimeAsync(ENTRY_STATUS_RETRY_DELAYS_MS.reduce((a, b) => a + b, 0))
 expect(await pending).toBeUndefined()
 expect(statusCalls(request)).toBe(ENTRY_STATUS_RETRY_DELAYS_MS.length + 1)
 expect(opts.mount).not.toHaveBeenCalled()
 expect(opts.navigate).toHaveBeenCalledOnce()
 expect(new URL(opts.navigate.mock.calls[0]![0]).searchParams.get('launchApp')).toBe('SYNO.SDS.ExampleApp.Instance')
})

test('a slow attempt is never failed by a retry clock', async () => {
 vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
 const opts = options()
 let answer!: (response: Response) => void
 const request = vi.fn<typeof globalThis.fetch>(() => new Promise(resolve => {answer = resolve}))
 const pending = bootEntryPage({...opts, fetch: request, requestTimeout: 3_600_000})
 await vi.advanceTimersByTimeAsync(60_000)
 expect(statusCalls(request)).toBe(1)
 expect(opts.navigate).not.toHaveBeenCalled()
 answer(json(account))
 const ctx = await pending
 expect(opts.mount).toHaveBeenCalledOnce()
 ctx!.host.dispose()
})

test.each(['session_required', 'app_privilege_denied', 'admin_required'])('a %s refusal falls back at once without a retry', async code => {
 const opts = options()
 const request = vi.fn<typeof globalThis.fetch>(async () => reply(code))
 expect(await bootEntryPage({...opts, fetch: request})).toBeUndefined()
 expect(statusCalls(request)).toBe(1)
 expect(opts.mount).not.toHaveBeenCalled()
 expect(opts.navigate).toHaveBeenCalledOnce()
})

test('an active portal alias takes the fallback to its branded sign-in, keeping link and the launch intent', async () => {
 const opts = options()
 const request = vi.fn<typeof globalThis.fetch>(async (url, init) => init?.method === 'GET'
  ? new Response('', {status: String(url) === 'https://localhost/example/' ? 200 : 404}) : reply('session_required'))
 expect(await bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}})).toBeUndefined()
 expect(opts.navigate).toHaveBeenCalledOnce()
 const url = new URL(opts.navigate.mock.calls[0]![0])
 expect(url.origin + url.pathname).toBe('https://localhost/example/')
 expect(url.searchParams.get('link')).toBe('/add')
 expect(url.searchParams.get('intake')).toBe('item')
 // After the alias sign-in DSM opens the App's window, which reads only DSM's launchParam.
 expect(launchOf(url)).toEqual({link: '/add', route: '/add', intake: 'item'})
 expect(request.mock.calls.find(([, init]) => init?.method === 'GET')![1]).toMatchObject({redirect: 'error', cache: 'no-store', mode: 'same-origin'})
})

test('an anonymous visitor reaches the alias sign-in with a hash route carried as link', async () => {
 const opts = {...options(), token: undefined, pageURL: 'https://localhost/webman/3rdparty/ExampleApp/app/entry.html?launch=9#/a/notes/42'}
 const request = vi.fn<typeof globalThis.fetch>(async url => String(url).includes('login.cgi')
  ? new Response(JSON.stringify({success: false})) : new Response('', {status: 200}))
 await bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}})
 expect(statusCalls(request)).toBe(0)
 const url = new URL(opts.navigate.mock.calls[0]![0])
 expect(url.pathname).toBe('/example/')
 expect(url.searchParams.get('link')).toBe('/a/notes/42')
 expect(url.searchParams.has('launch')).toBe(false)
 expect(url.hash).toBe('#/a/notes/42')
 expect(launchOf(url)).toEqual({route: '/a/notes/42'})
})

test('an entry below the alias falls back to that alias', async () => {
 const opts = {...options(), pageURL: 'https://localhost/notes/webman/3rdparty/ExampleApp/app/entry.html?link=%2Fx'}
 const request = vi.fn<typeof globalThis.fetch>(async (url, init) => init?.method === 'GET'
  ? new Response('', {status: String(url) === 'https://localhost/notes/' ? 200 : 404}) : reply('session_required'))
 await bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}})
 const url = new URL(opts.navigate.mock.calls[0]![0])
 expect(url.origin + url.pathname).toBe('https://localhost/notes/')
 expect(url.searchParams.get('link')).toBe('/x')
 expect(launchOf(url)).toEqual({link: '/x', route: '/x'})
})

test('a signed-out visitor with only a hash route reaches DSM App start with that route in launchParam', async () => {
 const opts = {...options(), token: undefined, pageURL: 'https://localhost/webman/3rdparty/ExampleApp/app/entry.html#/a/notes/42'}
 await bootEntryPage({...opts, fetch: vi.fn<typeof globalThis.fetch>(async () => new Response(JSON.stringify({success: false})))})
 const url = new URL(opts.navigate.mock.calls[0]![0])
 expect(url.origin + url.pathname).toBe('https://localhost/')
 expect(url.searchParams.get('launchApp')).toBe('SYNO.SDS.ExampleApp.Instance')
 expect(launchOf(url)).toEqual({route: '/a/notes/42'})
})

// Security: a sign-in redirect stays on this origin at a fixed path, carries
// only an App-relative route and never a session or secret.
const UNSAFE_ROUTES = ['//evil.example/x', '/\\evil.example', 'https://evil.example/', 'javascript:alert(1)', '/%2Fevil.example', '/%5Cevil.example', '/%252F%252Fevil.example', '%2F%2Fevil.example']
test.each([['the alias', 200], ['DSM App start', 404]])('a signed-out visitor sent to %s never carries an unsafe route or a credential', async (_name, aliasStatus) => {
 for(const bad of UNSAFE_ROUTES) {
  const page = new URL('https://localhost/webman/3rdparty/ExampleApp/app/entry.html')
  page.searchParams.set('link', bad)
  page.searchParams.set('route', bad)
  page.searchParams.set('SynoToken', 'secret-token')
  page.searchParams.set('_sid', 'secret-sid')
  page.hash = bad
  const opts = {...options(), token: undefined, pageURL: page.href}
  const request = vi.fn<typeof globalThis.fetch>(async url => String(url).includes('login.cgi') ? new Response(JSON.stringify({success: false})) : new Response('', {status: aliasStatus}))
  await bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}})
  const target = opts.navigate.mock.calls[0]![0]
  const url = new URL(target)
  expect(url.origin, bad).toBe('https://localhost')
  expect(['/example/', '/'], bad).toContain(url.pathname)
  expect(url.searchParams.get('link'), bad).toBeNull()
  expect(url.hash, bad).toBe('')
  const launch = launchOf(url)
  expect(launch.route, bad).toBeUndefined()
  expect(launch.link, bad).toBeUndefined()
  expect(target, bad).not.toMatch(/secret-token|secret-sid|SynoToken|_sid/i)
 }
})

test.each([
 ['an inactive alias', async () => new Response('', {status: 404})],
 ['an unreachable alias', async () => {throw new TypeError('Failed to fetch')}],
])('%s falls back to DSM App start', async (_name, alias) => {
 const opts = options()
 const request = vi.fn<typeof globalThis.fetch>(async (_url, init) => init?.method === 'GET' ? alias() : reply('session_required'))
 await bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}})
 const url = new URL(opts.navigate.mock.calls[0]![0])
 expect(url.pathname).toBe('/')
 expect(url.searchParams.get('launchParam')).toContain('link=%2Fadd')
})

test('a hung alias probe picks DSM App start after the probe wait', async () => {
 vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
 const opts = options()
 const request = vi.fn<typeof globalThis.fetch>(async (_url, init) => init?.method === 'GET' ? new Promise<Response>(() => {}) : reply('session_required'))
 const pending = bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}})
 await vi.advanceTimersByTimeAsync(PORTAL_FALLBACK_PROBE_MS - 1)
 expect(opts.navigate).not.toHaveBeenCalled()
 await vi.advanceTimersByTimeAsync(1)
 await pending
 expect(new URL(opts.navigate.mock.calls[0]![0]).searchParams.get('launchApp')).toBe('SYNO.SDS.ExampleApp.Instance')
})

const ingressURL = 'https://localhost/synopkgland/ExampleApp/'
const cgiURL = 'https://localhost/webman/3rdparty/ExampleApp/cgi/api.cgi'
const refusal = (code: string) => new Response(JSON.stringify({success: false, error: {code}}))

test('a declared daemon ingress reads app.status there, with the same token, and mounts without the CGI', async () => {
 const request = statusFetch(), opts = options()
 const ctx = await bootEntryPage({...opts, daemonIngress: '/synopkgland/ExampleApp/', fetch: request})
 expect(opts.navigate).not.toHaveBeenCalled()
 expect(opts.mount).toHaveBeenCalledOnce()
 expect(request).toHaveBeenCalledTimes(1)
 const [url, init] = request.mock.calls[0]!
 expect(url).toBe(ingressURL)
 expect(init).toMatchObject({method: 'POST', mode: 'same-origin', credentials: 'same-origin', redirect: 'error', headers: {'X-SYNO-TOKEN': 'sign-in'}})
 expect(JSON.parse(String(init?.body))).toEqual({method: 'app.status', params: {}})
 // The background check follows the same ingress; other calls keep the CGI.
 request.mockImplementation(async () => json({}))
 const abort = new AbortController()
 await ctx!.call('app.status', {}, {signal: abort.signal})
 await ctx!.call('items.list')
 expect(request.mock.calls.slice(1).map(call => call[0])).toEqual([ingressURL, cgiURL])
 expect(request.mock.calls[1]![1]?.headers).toMatchObject({'X-SYNO-TOKEN': 'sign-in'})
 expect(ctx!.files?.endpoint).toBe(cgiURL)
 ctx!.host.dispose()
})

test('under a portal alias the ingress stays at the origin root', async () => {
 history.replaceState(null, '', '/myalias/webman/3rdparty/ExampleApp/app/entry.html')
 const request = statusFetch()
 const ctx = await bootEntryPage({...options(), pageURL: 'https://localhost/myalias/webman/3rdparty/ExampleApp/app/entry.html', daemonIngress: '/synopkgland/ExampleApp/', fetch: request})
 expect(request.mock.calls[0]![0]).toBe(ingressURL)
 ctx!.host.dispose()
})

test('a daemon refusal is final: no CGI status, DSM sign-in', async () => {
 const request = vi.fn<typeof globalThis.fetch>(async () => refusal('session_required')), opts = options()
 expect(await bootEntryPage({...opts, daemonIngress: '/synopkgland/ExampleApp/', fetch: request})).toBeUndefined()
 expect(request.mock.calls.map(call => call[0])).toEqual([ingressURL])
 expect(opts.navigate).toHaveBeenCalledOnce()
})

test('a daemon that did not answer sends that status read to the CGI', async () => {
 for(const unavailable of [() => {throw new TypeError('network')}, () => new Response('', {status: 502}), () => new Response('', {status: 404})]) {
  localStorage.clear()
  const request = vi.fn<typeof globalThis.fetch>(async url => url === ingressURL ? unavailable() : json(account)), opts = options()
  const ctx = await bootEntryPage({...opts, daemonIngress: '/synopkgland/ExampleApp/', fetch: request})
  expect(request.mock.calls.map(call => call[0])).toEqual([ingressURL, cgiURL])
  expect(opts.mount).toHaveBeenCalledOnce()
  ctx!.host.dispose()
 }
})

test('the daemon busy reply retries at the ingress, never turning into a sign-out', async () => {
 vi.useFakeTimers()
 const request = vi.fn<typeof globalThis.fetch>()
  .mockImplementationOnce(async () => refusal('busy'))
  .mockImplementation(async () => json(account))
 const opts = options()
 const pending = bootEntryPage({...opts, daemonIngress: '/synopkgland/ExampleApp/', fetch: request})
 await vi.advanceTimersByTimeAsync(ENTRY_STATUS_RETRY_DELAYS_MS[0]!)
 const ctx = await pending
 expect(request.mock.calls.map(call => call[0])).toEqual([ingressURL, ingressURL])
 expect(opts.navigate).not.toHaveBeenCalled()
 ctx!.host.dispose()
})

test('an ingress other than the package path is refused before any request', async () => {
 for(const daemonIngress of ['/synopkgland/OtherApp/', '/synopkgland/exampleapp/', 'https://evil.example/synopkgland/ExampleApp/', '//evil.example/synopkgland/ExampleApp/', '/synopkgland/ExampleApp', '']) {
  const request = statusFetch()
  await expect(bootEntryPage({...options(), daemonIngress, fetch: request})).rejects.toThrow('Invalid daemon ingress')
  expect(request).not.toHaveBeenCalled()
  expect(() => createAppContext({packageId: 'ExampleApp', build: 'b', token: 't', pageURL: entryURL, daemonIngress, fetch: request})).toThrow('Invalid daemon ingress')
 }
})

// An entry cache written before the hint existed, or before the user changed
// DSM's display language, must not keep the App in the old language.
const enuAccount = {user: 'alice', admin: false, lang: 'enu', locale: 'en'}
const settle = () => new Promise(resolve => setTimeout(resolve, 0))

test('a cached sign-in still sends the DSM language hint and reloads once into the new language', async () => {
 const reload = vi.fn()
 const first = await bootEntryPage({...options(), fetch: vi.fn(async () => json(enuAccount)), reload})
 expect(first?.locale).toBe('en')
 first!.host.dispose()
 vi.mocked(readDsmLanguage).mockResolvedValue('cht')
 const request = statusFetch(), mount = vi.fn()
 const ctx = await bootEntryPage({...options(), fetch: request, mount, reload})
 // The cached account mounts first; the hint goes out in the background.
 expect(ctx?.locale).toBe('en')
 expect(mount).toHaveBeenCalledOnce()
 await settle()
 expect(request).toHaveBeenCalledOnce()
 expect(JSON.parse(String(request.mock.calls[0]![1]?.body))).toEqual({method: 'app.status', params: {dsmLanguage: 'cht'}})
 expect(reload).toHaveBeenCalledOnce()
 ctx!.host.dispose()
 // After the reload the cache answers the new language: no second reload.
 const after = await bootEntryPage({...options(), fetch: statusFetch(), reload})
 expect(after?.locale).toBe('zh-TW')
 await settle()
 expect(reload).toHaveBeenCalledOnce()
 after!.host.dispose()
 vi.mocked(readDsmLanguage).mockResolvedValue(undefined)
})

test('a cached sign-in keeps its language when DSM cannot be read, when nothing changed, or after user input', async () => {
 const reload = vi.fn()
 ;(await bootEntryPage({...options(), fetch: vi.fn(async () => json(enuAccount)), reload}))!.host.dispose()
 // An unreadable DSM language sends nothing and keeps the cached screen.
 const quiet = statusFetch()
 const unread = await bootEntryPage({...options(), fetch: quiet, reload})
 await settle()
 expect(quiet).not.toHaveBeenCalled()
 expect(unread?.locale).toBe('en')
 unread!.host.dispose()
 // A failing status never fails or reloads the mounted App.
 vi.mocked(readDsmLanguage).mockResolvedValue('cht')
 const refused = await bootEntryPage({...options(), fetch: vi.fn(async () => new Response(JSON.stringify({success: false, error: {code: 'session_required'}}))), reload})
 await settle()
 expect(refused?.locale).toBe('en')
 refused!.host.dispose()
 // Unchanged language: the hint is sent, nothing reloads.
 vi.mocked(readDsmLanguage).mockResolvedValue('enu')
 const same = await bootEntryPage({...options(), fetch: vi.fn(async () => json(enuAccount)), reload})
 await settle()
 same!.host.dispose()
 // The user already typed: the cache learns the language for the next open,
 // the open page is not reloaded under them.
 vi.mocked(readDsmLanguage).mockResolvedValue('cht')
 let answer!: (response: Response) => void
 const pending = vi.fn(() => new Promise<Response>(resolve => {answer = resolve}))
 const busy = await bootEntryPage({...options(), fetch: pending, reload})
 await settle()
 window.dispatchEvent(new KeyboardEvent('keydown', {key: 'a'}))
 answer(json(account))
 await settle()
 busy!.host.dispose()
 expect(reload).not.toHaveBeenCalled()
 const next = await bootEntryPage({...options(), fetch: statusFetch(), reload})
 expect(next?.locale).toBe('zh-TW')
 next!.host.dispose()
 vi.mocked(readDsmLanguage).mockResolvedValue(undefined)
})

const shortEntry = 'https://localhost/webman/3rdparty/ExampleApp/app/launch.html'
const statusOnly = () => vi.fn<typeof globalThis.fetch>(async (_url, init) => init?.method === 'POST' ? json(account) : new Response('', {status: 404}))

test('a page opened through its short path shows the short path, keeps the query and anchors URLs to the entry page', async () => {
 sessionStorage.clear()
 history.replaceState(null, '', `${shortEntry}?spl-short=go&link=%2Fadd#top`)
 const request = statusOnly(), opts = {...options(), pageURL: undefined, shortPath: '/go'}
 const ctx = await bootEntryPage({...opts, fetch: request})
 expect(`${location.pathname}${location.search}${location.hash}`).toBe('/go?link=%2Fadd#top')
 expect(document.querySelector('base')?.href).toBe(shortEntry)
 expect(request.mock.calls[0]![0]).toBe('https://localhost/webman/3rdparty/ExampleApp/cgi/api.cgi')
 expect(ctx?.launchParams).toEqual({link: '/add', route: '/add'})
 expect(ctx?.entryURL).toBe('https://localhost/go')
 ctx!.host.dispose()
})

test.each([
 ['another short path', '?spl-short=other&link=%2Fadd', '/go'],
 ['no declared short path', '?spl-short=go&link=%2Fadd', undefined],
 ['a marker that is not first', '?link=%2Fadd&spl-short=go', '/go'],
])('%s keeps the entry URL', async (_name, search, shortPath) => {
 sessionStorage.clear()
 history.replaceState(null, '', `${shortEntry}${search}`)
 const ctx = await bootEntryPage({...options(), pageURL: undefined, shortPath, fetch: statusOnly()})
 expect(location.pathname).toBe('/webman/3rdparty/ExampleApp/app/launch.html')
 expect(document.querySelector('base')).toBeNull()
 ctx!.host.dispose()
})

test('a short path survives a DSM sign-in in the same tab', async () => {
 sessionStorage.clear()
 history.replaceState(null, '', `${shortEntry}?spl-short=go&link=%2Fadd`)
 const opts = {...options(), pageURL: undefined, shortPath: '/go', token: undefined}
 await bootEntryPage({...opts, fetch: vi.fn(async () => new Response(JSON.stringify({success: false})))})
 const url = new URL(opts.navigate.mock.calls[0]![0])
 expect(url.searchParams.get('launchParam')).not.toContain('spl-short')
 // DSM's sign-in opens the entry page again, without the marker or the query.
 history.replaceState(null, '', shortEntry)
 document.head.innerHTML = ''
 const ctx = await bootEntryPage({...options(), pageURL: undefined, shortPath: '/go', fetch: statusOnly()})
 expect(`${location.pathname}${location.search}`).toBe('/go?link=%2Fadd')
 expect(ctx?.launchParams).toMatchObject({link: '/add'})
 ctx!.host.dispose()
})

test('the short path wins over rewriteToAlias', async () => {
 sessionStorage.clear()
 history.replaceState(null, '', `${shortEntry}?spl-short=go`)
 const request = vi.fn<typeof globalThis.fetch>(async (_url, init) => init?.method === 'POST' ? json(account) : new Response(''))
 const ctx = await bootEntryPage({...options(), pageURL: undefined, shortPath: '/go', portal: {defaultAlias: 'example'}, rewriteToAlias: true, fetch: request})
 await new Promise(resolve => setTimeout(resolve, 0))
 expect(`${location.pathname}${location.search}`).toBe('/go')
 expect(request.mock.calls.some(([url]) => String(url) === 'https://localhost/example/')).toBe(false)
 ctx!.host.dispose()
})

test('a prefix page uses its asset base for preloads and the DSM origin for API calls', async () => {
 sessionStorage.clear()
 const path = '/reader/book/chapter?link=%2Fadd#section'
 history.replaceState(null, '', path)
 document.head.innerHTML = '<base href="/webman/3rdparty/ExampleApp/app/build/m/">'
 const fetch = statusOnly()
 const ctx = await bootEntryPage({...options(), pageURL: undefined, pathPages: ['/reader/'], appBundle: './chapter.js', appIcon: './icon.png', fetch, rewriteToAlias: true, portal: {defaultAlias: 'example'}})
 expect(document.querySelector<HTMLLinkElement>('link[rel=modulepreload]')?.href).toBe('https://localhost/webman/3rdparty/ExampleApp/app/build/m/chapter.js')
 expect(fetch.mock.calls[0]![0]).toBe('https://localhost/webman/3rdparty/ExampleApp/cgi/api.cgi')
 expect(ctx?.entryURL).toBe('https://localhost/reader/')
 expect(ctx?.appIcon).toBe('https://localhost/webman/3rdparty/ExampleApp/app/build/m/icon.png')
 expect(ctx?.launchParams).toMatchObject({link: '/add'})
 expect(location.href).toBe(`https://localhost${path}`)
 ctx!.host.dispose()
})

test('the authenticated launcher navigates back to the complete prefix URL once', async () => {
 sessionStorage.clear()
 const path = '/reader/book/chapter?sort=1&sort=2#section'
 history.replaceState(null, '', path)
 const opts = {...options(), pageURL: undefined, pathPages: ['/reader/']}
 await bootEntryPage({...opts, token: undefined, fetch: vi.fn(async () => new Response(JSON.stringify({success: false})))})
 expect(new URL(opts.navigate.mock.calls[0]![0]).searchParams.get('launchApp')).toBe('SYNO.SDS.ExampleApp.Instance')
 expect(opts.navigate.mock.calls[0]![0]).not.toContain('chapter')
 history.replaceState(null, '', shortEntry)
 const ctx = await bootEntryPage({...opts, fetch: statusOnly()})
 expect(ctx).toBeUndefined()
 expect(opts.navigate.mock.calls[1]![0]).toBe(`https://localhost${path}`)
 const later = await bootEntryPage({...opts, fetch: statusOnly()})
 expect(later?.entryURL).toBe(shortEntry)
 expect(opts.navigate).toHaveBeenCalledTimes(2)
 later!.host.dispose()
})
