import {createClient, type SessionRetryOperation} from '../client'
import type {Context, LaunchParams} from '../context'
import {createDsmSession} from '../dsm/session.js'
import {format, uiLocale, type Message} from '../i18n'
import {createNativeHost, nativeLaunchParams, onNativeLaunch} from '../mobile/index'
import {createPortalAccess} from '../portal/access'
import {createSignInCache} from './cache'

export interface AppContextOptions extends Partial<Omit<Context, 'call' | 'batch' | 'files' | 'portal'>> {
 packageId: string
 build: string
 token?: string
 session?: ReturnType<typeof createDsmSession>
 /** Absolute entry URL, captured before an optional portal rewrite. */
 entryURL?: string
 fetch?: typeof globalThis.fetch
 portal?: {defaultAlias: string; entry?: boolean}
 portalAccess?: Context['portal']
 messages?: Readonly<Record<string, Readonly<Record<string, Message>>>>
 sessionRetryOperations?: Readonly<Record<string, SessionRetryOperation>>
}

export function entryURLs(packageId: string, href: string) {
 if(!/^[A-Za-z][A-Za-z0-9]{1,63}$/.test(packageId)) throw new Error('Invalid package id')
 const at = new URL(href)
 const marker = `/webman/3rdparty/${packageId}/`
 const index = at.pathname.indexOf(marker)
 const prefix = index < 0 ? new URL('./', at).pathname.replace(/\/$/, '') : at.pathname.slice(0, index)
 const base = `${at.origin}${prefix}/`
 return {base, cgi: `${base}webman/3rdparty/${packageId}/cgi/api.cgi`, entry: `${base}webman/3rdparty/${packageId}/app/entry.html`, webApi: `${base}webapi/entry.cgi`, token: `${base}webman/login.cgi?enable_syno_token=yes`}
}

/** One ctx builder for full-page entries and CLI windows with their existing host/lifecycle adapters. */
export function createAppContext(options: AppContextOptions): Context {
 const at = new URL(options.entryURL ?? location.href)
 const urls = entryURLs(options.packageId, at.href)
 const request = options.fetch ?? globalThis.fetch
 let current = options.token
 const session = options.session ?? createDsmSession({read: () => current, write: value => {current = value},
  fetch: (_url, init) => request(urls.token, init)})
 const client = createClient({endpoint: urls.cgi, requestTimeout: options.requestTimeout, fetch: request,
  sessionRetry: {session, operations: options.sessionRetryOperations ?? {}}})
 const locale = options.locale || uiLocale(options.lang || globalThis.navigator?.language || 'en')
 const webApi: NonNullable<Context['webApi']> = options.webApi ?? (async (api, method, version, params) => {
  const body = new URLSearchParams({api, method, version: String(version)})
  for(const [key, value] of Object.entries(params)) body.set(key, typeof value === 'string' ? value : JSON.stringify(value))
  const response = await request(urls.webApi, {method: 'POST', mode: 'same-origin', credentials: 'same-origin', redirect: 'error', cache: 'no-store',
   headers: {'X-SYNO-TOKEN': session.token() ?? ''}, body})
  const reply = await response.json()
  if(response.ok && reply?.success === true) return reply.data
  throw {code: `dsm_${reply?.error?.code ?? 'error'}`}
 })
 const launchDisposers = new Set<() => void>()
 const baseHost = options.host ?? createNativeHost({dsmStandalone: true, dsmLanguage: options.lang ?? null, appLocale: locale})
 const cache = createSignInCache(options.packageId)
 if(options.user?.name && session.token()) cache.set('entry', session.token()!, {
  user: options.user.name, admin: options.user.isAdmin, lang: options.lang, locale,
 })
 const host = {...baseHost,
  ...(!options.host ? {signOut() {
   cache.clear()
   void webApi('SYNO.API.Auth', 'logout', 6, {}).catch(() => {}).finally(() => {location.replace(new URL('/', at).href)})
   return {status: 'requested'} as const
  }} : {}),
  dispose() {for(const dispose of [...launchDisposers]) dispose(); baseHost.dispose()},
 }
 const onLaunch: NonNullable<Context['onLaunch']> = listener => {
  const native = onNativeLaunch(listener)
  const dsm = options.onLaunch?.(listener)
  let disposed = false
  const dispose = () => {if(disposed) return; disposed = true; native(); dsm?.(); launchDisposers.delete(dispose)}
  launchDisposers.add(dispose)
  return dispose
 }
 const launchParams = options.launchParams ?? entryLaunchParams(at.search)
 const t: NonNullable<Context['t']> = (key, params = {}) => {
  const message = options.messages?.[locale]?.[key] ?? options.messages?.en?.[key]
  const values = Object.fromEntries(Object.entries(params).map(([name, value]) => [name, typeof value === 'number' ? value : String(value)]))
  return message === undefined ? key : format(message, values, locale)
 }
 const ctx: Context = {
  locale, lang: options.lang, t: options.t ?? t, host, fullPage: options.fullPage ?? true,
  requestTimeout: options.requestTimeout, displayName: options.displayName, appIcon: options.appIcon ? new URL(options.appIcon, at).href : undefined,
  user: options.user, appWindow: options.appWindow, executor: options.executor, authorization: options.authorization, delegate: options.delegate,
  call: client.call, batch: client.batch, webApi, files: {endpoint: urls.cgi, token: session.token},
  reopen: options.reopen ?? (() => () => {}), launchParams, onLaunch,
  uiUpdate: options.uiUpdate ?? {async check(signal) {
   try {
    const url = new URL(urls.entry)
    url.searchParams.set('launch', String(Date.now()))
    const response = await request(url.href, {cache: 'no-store', mode: 'same-origin', redirect: 'error', credentials: 'same-origin', signal})
    if(!response.ok) return false
    const doc = new DOMParser().parseFromString(await response.text(), 'text/html')
    const build = doc.querySelector('meta[name="synopkgland-build"]')?.getAttribute('content')
    return !!build && !!options.build && build !== options.build
   } catch {return false}
  }},
 }
 ctx.portal = options.portalAccess ?? (options.portal ? createPortalAccess({appId: `SYNO.SDS.${options.packageId}.Instance`,
  defaultAlias: options.portal.defaultAlias, entry: options.portal.entry, locale, location: at,
  call: client.call, send: webApi, isAdmin: () => ctx.user?.isAdmin === true,
  launch: app => {host.openApp(app)},
 }) : undefined)
 return ctx
}

/** CLI-compatible bounded launch text, with native link → route normalization. */
export function entryLaunchParams(search: string): LaunchParams {
 const out: Record<string, string> = Object.create(null)
 const query = new URLSearchParams(search)
 let count = 0
 query.forEach((value, key) => {
  if(count >= 32 || ['launch', 'className', 'instanceName', 'dsm_version', 'title'].includes(key) || !/^[A-Za-z][A-Za-z0-9_.-]{0,63}$/.test(key)) return
  if(value.length > 2048 || /[\u0000-\u001f\u007f]/.test(value)) return
  out[key] = value
  count++
 })
 const native = nativeLaunchParams({link: out.link, intake: out.intake})
 // The native validator owns these values, including route validation.
 delete out.link; delete out.intake
 return Object.freeze({...out, ...native})
}
