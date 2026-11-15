// @vitest-environment jsdom
// @vitest-environment-options {"url":"https://localhost/"}
import {afterEach, beforeEach, expect, test, vi} from 'vitest'
import type {Context} from '../context'
import {bootEntryPage, createAppContext, entryLaunchParams, ENTRY_STATUS_RETRY_DELAYS_MS, PORTAL_FALLBACK_PROBE_MS} from '../entry'

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

test('uiUpdate compares the exact CLI hash and handles missing metadata, refusals and offline', async () => {
 let html = '<meta name="synopkgland-build" content="abc">'
 const request = vi.fn<typeof globalThis.fetch>(async () => new Response(html))
 const ctx = createAppContext({packageId: 'ExampleApp', build: 'abc', token: 'token', pageURL: entryURL, fetch: request})
 expect(await ctx.uiUpdate!.check()).toBe(false)
 html = '<meta content="abcd" name="synopkgland-build">'
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
 let build = 'abc'
 const request = vi.fn<typeof globalThis.fetch>(async url => String(url).includes('entry.json')
  ? new Response(JSON.stringify({v: 1, build})) : new Response('<meta name="synopkgland-build" content="other">'))
 const ctx = createAppContext({packageId: 'ExampleApp', build: 'abc', token: 'token', pageURL: entryURL, fetch: request})
 expect(await ctx.uiUpdate!.check()).toBe(false)
 build = 'def'
 expect(await ctx.uiUpdate!.check()).toBe(true)
 expect(request.mock.calls.every(([url]) => String(url).includes('entry.json'))).toBe(true)
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

test.each(['https://outside.example/app.js', '//outside.example/app.js', 'javascript:alert(1)', 'data:text/javascript,alert(1)', 'https://user:pass@localhost/app.js'])('refuses an unsafe bundle URL before preload or identity: %s', async appBundle => {
 const opts = options(), request = statusFetch()
 await expect(bootEntryPage({...opts, appBundle, fetch: request})).rejects.toThrow('Invalid App bundle URL')
 expect(document.querySelector('link[rel=modulepreload]')).toBeNull()
 expect(request).not.toHaveBeenCalled()
 expect(opts.mount).not.toHaveBeenCalled()
})

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
 expect(opts.navigate).toHaveBeenCalledExactlyOnceWith('https://localhost/example/?link=%2Fadd&intake=item')
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
})

test('an entry below the alias falls back to that alias', async () => {
 const opts = {...options(), pageURL: 'https://localhost/notes/webman/3rdparty/ExampleApp/app/entry.html?link=%2Fx'}
 const request = vi.fn<typeof globalThis.fetch>(async (url, init) => init?.method === 'GET'
  ? new Response('', {status: String(url) === 'https://localhost/notes/' ? 200 : 404}) : reply('session_required'))
 await bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}})
 expect(opts.navigate).toHaveBeenCalledExactlyOnceWith('https://localhost/notes/?link=%2Fx')
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
