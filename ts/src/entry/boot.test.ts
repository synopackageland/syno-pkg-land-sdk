// @vitest-environment jsdom
// @vitest-environment-options {"url":"https://localhost/"}
import {afterEach, beforeEach, expect, test, vi} from 'vitest'
import type {Context} from '../context'
import {bootEntryPage, createAppContext, entryLaunchParams} from '../entry'

const entryURL = 'https://localhost/webman/3rdparty/ExampleApp/app/entry.html?launch=123&link=%2Fadd&intake=item'
const account = {user: 'alice', admin: false, lang: 'cht', locale: 'zh-TW'}
const json = (data: unknown) => new Response(JSON.stringify({success: true, data}))
const root = () => document.querySelector<HTMLElement>('.dsmapp-root')!
const statusFetch = () => vi.fn<typeof globalThis.fetch>(async () => json(account))
const options = () => ({packageId: 'ExampleApp', build: 'build-a', token: 'sign-in', entryURL, root: root(), mount: vi.fn<(root: HTMLElement, ctx: Context) => unknown>(), navigate: vi.fn<(url: string) => void>()})

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
  const ctx = await bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}})
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
 const ctx = createAppContext({packageId: 'ExampleApp', build: 'abc', token: 'token', entryURL, fetch: request})
 expect(await ctx.uiUpdate!.check()).toBe(false)
 html = '<meta content="abcd" name="synopkgland-build">'
 expect(await ctx.uiUpdate!.check()).toBe(true)
 html = '<script src="app.js?v=another-build"></script>'
 expect(await ctx.uiUpdate!.check()).toBe(false)
 request.mockRejectedValueOnce(new Error('offline'))
 expect(await ctx.uiUpdate!.check()).toBe(false)
 expect(String(request.mock.calls[0]![0])).toContain('/app/entry.html?launch=')
 expect(request.mock.calls[0]![1]).toMatchObject({cache: 'no-store'})
 ctx.host.dispose()
})

test('a hung portal probe does not hold up mount or navigate away', async () => {
 const opts = options()
 const ctx = await bootEntryPage({...opts, portal: {defaultAlias: 'example'}, fetch: async (_url, init) => {
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
 const ctx = createAppContext({packageId: 'ExampleApp', build: 'a', entryURL, onLaunch: dsm})
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
 const ctx = await bootEntryPage({...options(), portal: {defaultAlias: 'example'}, fetch: async (_url, init) => {
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
 const ctx = createAppContext({packageId: 'ExampleApp', build: 'a', entryURL, token: 'token', fetch: request,
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
