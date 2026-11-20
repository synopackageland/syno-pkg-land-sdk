// @vitest-environment jsdom
// @vitest-environment-options {"url":"https://localhost/"}
import {afterEach, beforeEach, expect, test, vi} from 'vitest'
import type {Context, PortalStatus} from '../context'
import {bootEntryPage, createAppContext, entryLaunchParams, LAUNCH_HANDOFF_MAX_AGE_MS} from '../entry'
import {createPortalAccess} from '../portal/access'

const pageURL = 'https://localhost/webman/3rdparty/ExampleApp/app/entry.html'
const account = {user: 'alice', admin: false, lang: 'enu', locale: 'en'}
const json = (data: unknown) => new Response(JSON.stringify({success: true, data}))
const refuse = () => new Response(JSON.stringify({success: false, error: {code: 'session_required'}}))
const root = () => document.querySelector<HTMLElement>('.dsmapp-root')!
const boot = (extra: Record<string, unknown> = {}) => ({packageId: 'ExampleApp', build: 'b', token: 'sign-in', pageURL, root: root(),
 mount: vi.fn<(root: HTMLElement, ctx: Context) => unknown>(), navigate: vi.fn<(url: string) => void>(), ...extra})

beforeEach(() => {
 localStorage.clear(); sessionStorage.clear()
 document.body.innerHTML = '<div class="dsmapp-root"></div>'
 history.replaceState(null, '', pageURL)
})
afterEach(() => {vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks()})

test('ctx.entryURL is the entry page, the declared short path, or a same-origin override', () => {
 const make = (extra: Record<string, unknown>) => createAppContext({packageId: 'ExampleApp', build: 'b', token: 't', pageURL: `${pageURL}?link=%2Fx#/x`, ...extra})
 // The launcher's page, also from the earlier entry.html, which browsers may keep with an old build.
 const launchPage = 'https://localhost/webman/3rdparty/ExampleApp/app/launch.html'
 expect(make({}).entryURL).toBe(launchPage)
 expect(make({pageURL: launchPage}).entryURL).toBe(launchPage)
 expect(make({shortPath: '/notes'}).entryURL).toBe('https://localhost/notes')
 expect(make({shortPath: '/Not Valid'}).entryURL).toBe(launchPage)
 expect(make({shortPath: '//evil.example'}).entryURL).toBe(launchPage)
 expect(make({entryURL: 'https://localhost/notes?keep=1#drop'}).entryURL).toBe('https://localhost/notes?keep=1')
 expect(make({entryURL: 'https://evil.example/notes', shortPath: '/notes'}).entryURL).toBe('https://localhost/notes')
 expect(make({pageURL: 'https://localhost/notes/webman/3rdparty/ExampleApp/app/entry.html'}).entryURL)
  .toBe('https://localhost/notes/webman/3rdparty/ExampleApp/app/launch.html')
})

test('the entry keeps its own address by default, even with an active alias', async () => {
 const opts = boot()
 const request = vi.fn<typeof globalThis.fetch>(async (_url, init) => init?.method === 'GET' ? new Response('') : json(account))
 const ctx = await bootEntryPage({...opts, fetch: request, portal: {defaultAlias: 'example'}})
 await new Promise(resolve => setTimeout(resolve, 0))
 expect(location.pathname).toBe('/webman/3rdparty/ExampleApp/app/entry.html')
 expect(request.mock.calls.some(([url]) => String(url) === 'https://localhost/example/')).toBe(false)
 ctx!.host.dispose()
})

test('outside the desktop, openApp and File Station open DSM launch links in a new tab', () => {
 const open = vi.fn((_url: string, _target?: string, _features?: string) => null)
 vi.stubGlobal('open', open)
 const ctx = createAppContext({packageId: 'ExampleApp', build: 'b', token: 't', pageURL})
 expect(ctx.host.openApp('SYNO.SDS.PkgManApp.Instance')).toEqual({status: 'requested'})
 expect(open).toHaveBeenLastCalledWith('https://localhost/?launchApp=SYNO.SDS.PkgManApp.Instance&launchParam=', '_blank', 'noopener')
 expect(ctx.host.openFileManager('/volume1/media/a b')).toEqual({status: 'requested', action: 'locate-folder'})
 let url = new URL(open.mock.lastCall![0])
 expect(url.searchParams.get('launchApp')).toBe('SYNO.SDS.App.FileStation3.Instance')
 expect(url.searchParams.get('launchParam')).toBe('opendir=%2Fmedia%2Fa%20b')
 expect(ctx.host.revealFile('/media/a.txt')).toEqual({status: 'requested', action: 'highlight-file'})
 url = new URL(open.mock.lastCall![0])
 expect(url.searchParams.get('launchParam')).toBe('openfile=%2Fmedia%2Fa.txt')
 for(const path of ['/volume1', 'media/a', '/media/../x', '/media//x']) expect(ctx.host.revealFile(path)).toEqual({status: 'invalid-input'})
 expect(ctx.host.openApp('not an app')).toEqual({status: 'invalid-input'})
 expect(ctx.host.openFile('/media/a.txt').status).toBe('unsupported')
 expect(open).toHaveBeenCalledTimes(3)
 ctx.host.dispose()
})

test('the portal panel opens Control Panel Login Portal through a launch link', () => {
 const open = vi.fn((_url: string, _target?: string, _features?: string) => null)
 vi.stubGlobal('open', open)
 const ctx = createAppContext({packageId: 'ExampleApp', build: 'b', token: 't', pageURL, fetch: vi.fn(async () => json({state: 'inactive'})), portal: {defaultAlias: 'example'}})
 ctx.portal!.openControlPanel()
 const url = new URL(open.mock.lastCall![0])
 expect(url.searchParams.get('launchApp')).toBe('SYNO.SDS.AdminCenter.Application')
 expect(url.searchParams.get('launchParam')).toContain('fn=')
 ctx.host.dispose()
})

test('a native shell keeps its own host', () => {
 vi.stubGlobal('SynoPkgNative', {protocol: 1, shell: {name: 'Test shell', version: '1', platform: 'android'},
  caps: {download: false, openExternal: false, intake: true, camera: false}, post: vi.fn()})
 const open = vi.fn()
 vi.stubGlobal('open', open)
 const ctx = createAppContext({packageId: 'ExampleApp', build: 'b', token: 't', pageURL})
 expect(ctx.host.openApp('SYNO.SDS.PkgManApp.Instance').status).toBe('unsupported')
 expect(open).not.toHaveBeenCalled()
 ctx.host.dispose()
})

test('a launch DSM forwards to a url launcher is read in both of its forms; the page query wins', () => {
 const chars = [...'link=%2Fa%2Fb&intake=item&x=1%3D2'].map((c, i) => `${i}=${c}`).join('&')
 expect(entryLaunchParams(`?launchParam=${encodeURIComponent(chars)}`)).toEqual({x: '1=2', link: '/a/b', route: '/a/b', intake: 'item'})
 expect(entryLaunchParams(`?launchParam=${encodeURIComponent('link=%2Fp')}`)).toEqual({link: '/p', route: '/p'})
 expect(entryLaunchParams(`?link=%2Fown&launchParam=${encodeURIComponent('link=%2Fp&y=2')}`)).toEqual({y: '2', link: '/own', route: '/own'})
 expect(entryLaunchParams(`?launchParam=${encodeURIComponent('0=a&2=b')}`)).toEqual({})
 expect(entryLaunchParams('?launchParam=%E0%A4%A')).toEqual({})
})

test('a sign-in hand-off keeps the route for the entry that opens after DSM drops the query', async () => {
 const first = boot({token: undefined, pageURL: `${pageURL}?intake=share#/a/notes/42`})
 await bootEntryPage({...first, fetch: vi.fn(async () => new Response(JSON.stringify({success: false})))})
 expect(first.navigate).toHaveBeenCalledOnce()
 // DSM's sign-in and its url launcher redirect open the bare entry page.
 const second = boot()
 const ctx = await bootEntryPage({...second, fetch: vi.fn(async () => json(account))})
 expect(ctx?.launchParams).toEqual({link: '/a/notes/42', route: '/a/notes/42', intake: 'share'})
 expect(new URL(location.href).searchParams.get('link')).toBe('/a/notes/42')
 expect(sessionStorage.length).toBe(0)
 ctx!.host.dispose()
 // Used once.
 history.replaceState(null, '', pageURL)
 const third = await bootEntryPage({...boot(), fetch: vi.fn(async () => json(account))})
 expect(third?.launchParams).toEqual({})
 third!.host.dispose()
})

test('an own launch, an old hand-off or a malformed one is not restored', async () => {
 const store = (value: unknown) => sessionStorage.setItem('synopkgland.ExampleApp.launch', JSON.stringify(value))
 store({v: 1, at: Date.now(), link: '/kept'})
 let ctx = await bootEntryPage({...boot({pageURL: `${pageURL}?link=%2Fown`}), fetch: vi.fn(async () => json(account))})
 expect(ctx?.launchParams).toEqual({link: '/own', route: '/own'})
 expect(sessionStorage.length).toBe(0)
 ctx!.host.dispose()
 for(const value of [{v: 1, at: Date.now() - LAUNCH_HANDOFF_MAX_AGE_MS - 1, link: '/old'}, {v: 1, at: Date.now(), link: '//evil'}, {v: 2, at: Date.now(), link: '/x'}]) {
  store(value)
  ctx = await bootEntryPage({...boot(), fetch: vi.fn(async () => json(account))})
  expect(ctx?.launchParams).toEqual({})
  ctx!.host.dispose()
 }
})

test('a refusal keeps the launch too, before the portal sign-in', async () => {
 const opts = boot({pageURL: `${pageURL}?link=%2Fx`})
 await bootEntryPage({...opts, portal: {defaultAlias: 'example'}, fetch: vi.fn(async (_url, init) => init?.method === 'GET' ? new Response('') : refuse())})
 const url = new URL(opts.navigate.mock.calls[0]![0])
 expect(url.origin + url.pathname).toBe('https://localhost/example/')
 expect(url.searchParams.get('link')).toBe('/x')
 expect(url.searchParams.get('launchParam')).toBe('link=%2Fx&route=%2Fx')
 expect(JSON.parse(sessionStorage.getItem('synopkgland.ExampleApp.launch')!)).toMatchObject({v: 1, link: '/x'})
})

test('the plain portal panel renders the status and applies an alias', async () => {
 let status: PortalStatus['state'] = 'inactive'
 const send = vi.fn(async (api: string, method: string) => {
  if(api === 'SYNO.API.Info') return {'SYNO.Core.AppPortal': {maxVersion: 2}}
  if(method === 'set') {status = 'active'; return {}}
  return {id: 'SYNO.SDS.ExampleApp.Instance', alias: status === 'active' ? 'example' : ''}
 })
 const access = createPortalAccess({appId: 'SYNO.SDS.ExampleApp.Instance', defaultAlias: 'example', locale: 'en', location: {protocol: 'https:', host: 'nas'},
  call: vi.fn(async () => ({})), send, isAdmin: () => true, launch: vi.fn()})
 const element = document.createElement('section')
 const panel = access.mountPanel!(element)
 await vi.waitFor(() => expect(element.querySelector('input')).not.toBeNull())
 expect(element.querySelector('input')!.value).toBe('example')
 element.querySelector('form')!.dispatchEvent(new Event('submit'))
 await vi.waitFor(() => expect(element.querySelector('a')?.getAttribute('href')).toBe('https://nas/example/'))
 panel.dispose()
 expect(element.childElementCount).toBe(0)
 const reader = createPortalAccess({appId: 'SYNO.SDS.ExampleApp.Instance', defaultAlias: 'example', locale: 'zh-TW', location: {protocol: 'https:', host: 'nas'},
  call: vi.fn(async () => ({state: 'inactive'})), send, isAdmin: () => false, launch: vi.fn()})
 const other = document.createElement('section')
 reader.mountPanel!(other)
 await vi.waitFor(() => expect(other.querySelector('p')?.textContent).toBe(reader.messages.askAdmin))
})
