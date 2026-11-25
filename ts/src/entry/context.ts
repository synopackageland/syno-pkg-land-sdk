import {createClient, type SessionRetryOperation} from '../client'
import type {CallOptions, Context, LaunchParams} from '../context'
import {createDsmSession} from '../dsm/session.js'
import {format, uiLocale, type Message} from '../i18n'
import {createNativeHost, nativeLaunchParams, onNativeLaunch} from '../mobile/index'
import {createPortalAccess} from '../portal/access'
import {createSignInCache} from './cache'
import {dsmLinkOpener, withDsmLinks} from './dsm-links'

export interface AppContextOptions extends Partial<Omit<Context, 'call' | 'batch' | 'files' | 'portal'>> {
 packageId: string
 build: string
 token?: string
 session?: ReturnType<typeof createDsmSession>
 /** Absolute URL of the page that booted, captured before any history
 * rewrite; defaults to location.href. CGI, WebAPI and update URLs follow it. */
 pageURL?: string
 /** The App's short path on DSM's origin (ui.short_paths, DSM 7.2+), such as
 * /notes; ctx.entryURL is then that path instead of the entry page. */
 shortPath?: string
 fetch?: typeof globalThis.fetch
 portal?: {defaultAlias: string; entry?: boolean}
 portalAccess?: Context['portal']
 messages?: Readonly<Record<string, Readonly<Record<string, Message>>>>
 sessionRetryOperations?: Readonly<Record<string, SessionRetryOperation>>
 /** The manifest's daemon.ingress path, exactly `/synopkgland/<packageId>/`,
  * which the CLI writes into the generated entry. When present, app.status
  * goes to the App's daemon on the page's origin instead of a CGI process;
  * every other call keeps the CGI. Never take it from the page URL. */
 daemonIngress?: string
}

/** The build hash the CLI writes into entry.json and the page it serves. */
const CLI_BUILD = /^[0-9a-f]{64}$/
const SHORT_PATH = /^\/[a-z0-9]+(?:-[a-z0-9]+)*$/

/** The link to share or bookmark: an explicit same-origin entryURL, else the
 * short path on this origin, else the entry page (on its alias prefix, if any). */
function shareableEntryURL(options: AppContextOptions, at: URL, entry: string): string {
 if(typeof options.entryURL === 'string') {
  try {
   const given = new URL(options.entryURL, at)
   if(given.origin === at.origin && !given.username && !given.password) {given.hash = ''; return given.href}
  } catch {/* Fall back to the derived link */}
 }
 if(typeof options.shortPath === 'string' && SHORT_PATH.test(options.shortPath)) return new URL(options.shortPath, at.origin).href
 return entry
}

/** The entry page the CLI's launcher opens. Browsers keep DSM's package files
 * for years without asking, and older CLIs put the build in entry.html,
 * so a browser that opened it keeps that build; launch.html never had
 * other bytes. entry.html stays as the same page for older links. */
const ENTRY_PAGE = 'launch.html'

export function entryURLs(packageId: string, href: string) {
 if(!/^[A-Za-z][A-Za-z0-9]{1,63}$/.test(packageId)) throw new Error('Invalid package id')
 const at = new URL(href)
 const marker = `/webman/3rdparty/${packageId}/`
 const index = at.pathname.indexOf(marker)
 const prefix = index < 0 ? new URL('./', at).pathname.replace(/\/$/, '') : at.pathname.slice(0, index)
 const base = `${at.origin}${prefix}/`
 return {base, cgi: `${base}webman/3rdparty/${packageId}/cgi/api.cgi`, entry: `${base}webman/3rdparty/${packageId}/app/${ENTRY_PAGE}`, webApi: `${base}webapi/entry.cgi`, token: `${base}webman/login.cgi?enable_syno_token=yes`}
}

/** The same-origin status endpoint, or undefined without daemon.ingress.
 * DSM's nginx serves the ingress at the origin root in its own server
 * blocks, so a portal alias prefix is not part of it. */
export function statusEndpoint(packageId: string, href: string, daemonIngress: string | undefined): string | undefined {
 if(daemonIngress === undefined) return undefined
 if(!/^[A-Za-z][A-Za-z0-9]{1,63}$/.test(packageId) || daemonIngress !== `/synopkgland/${packageId}/`) throw new Error('Invalid daemon ingress')
 return new URL(daemonIngress, new URL(href).origin).href
}

/** Statuses at which the daemon cannot have answered: nginx has no such
 * location (404) or could not reach the daemon (502–504). */
const DAEMON_UNAVAILABLE = new Set([404, 502, 503, 504])

/** A fetch for app.status: the daemon ingress first, then the CGI when the
 * daemon did not answer. app.status only reads, so a second send is safe.
 * A decoded reply from the daemon, a refusal or busy included, is final.
 * A missing location keeps the CGI for the rest of the page. */
export function statusFetch(request: typeof globalThis.fetch, ingress: string, cgi: string): typeof globalThis.fetch {
 let missing = false
 return async (_input, init) => {
  if(missing) return request(cgi, init)
  let response: Response
  try {response = await request(ingress, init)} catch(error) {
   if(init?.signal?.aborted) throw error
   return request(cgi, init)
  }
  if(init?.signal?.aborted || !DAEMON_UNAVAILABLE.has(response.status)) return response
  if(response.status === 404) missing = true
  return request(cgi, init)
 }
}

/** One ctx builder for full-page entries and CLI windows with their existing host/lifecycle adapters. */
export function createAppContext(options: AppContextOptions): Context {
 const at = new URL(options.pageURL ?? location.href)
 const urls = entryURLs(options.packageId, at.href)
 const request = options.fetch ?? globalThis.fetch
 let current = options.token
 const session = options.session ?? createDsmSession({read: () => current, write: value => {current = value},
  fetch: (_url, init) => request(urls.token, init)})
 const sessionRetry = {session, operations: options.sessionRetryOperations ?? {}}
 const client = createClient({endpoint: urls.cgi, requestTimeout: options.requestTimeout, fetch: request, sessionRetry})
 // app.status, including the EntryGuard's background check, follows the
 // manifest's daemon ingress with the same cookie, token, timeout and signal.
 const ingress = statusEndpoint(options.packageId, at.href, options.daemonIngress)
 const status = ingress && createClient({endpoint: ingress, requestTimeout: options.requestTimeout, fetch: statusFetch(request, ingress, urls.cgi), sessionRetry})
 const call: Context['call'] = status ? <T,>(method: string, params?: Record<string, unknown>, callOptions?: CallOptions) =>
  (method === 'app.status' ? status : client).call<T>(method, params, callOptions) : client.call
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
 // Outside DSM's desktop, DSM Apps open through DSM's launch link on this origin.
 const openDsm = dsmLinkOpener(at.origin)
 const baseHost = options.host ?? withDsmLinks(createNativeHost({dsmStandalone: true, dsmLanguage: options.lang ?? null, appLocale: locale}), openDsm)
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
  call, batch: client.batch, webApi, files: {endpoint: urls.cgi, token: session.token},
  reopen: options.reopen ?? (() => () => {}), launchParams, onLaunch,
  entryURL: shareableEntryURL(options, at, urls.entry),
  uiUpdate: options.uiUpdate ?? {async check(signal) {
   const read = async (name: string) => {
    const url = new URL(name, urls.entry)
    url.searchParams.set('launch', String(Date.now()))
    const response = await request(url.href, {cache: 'no-store', mode: 'same-origin', redirect: 'error', credentials: 'same-origin', signal})
    return response.ok ? response : undefined
   }
   // Only two CLI build hashes can say the UI changed. Anything else, such
   // as a page built by other tooling or an answer that is not the App's
   // entry files (a gateway, a sign-in page, an error), is unknown: no notice.
   if(!CLI_BUILD.test(options.build)) return false
   try {
    // The CLI's fixed entry page names the build in entry.json; older entry
    // pages carry it in their own meta.
    let build: unknown
    const pointer = await read('entry.json').catch(() => undefined)
    if(pointer) {
     try {
      const value = await pointer.json() as {v?: unknown; build?: unknown} | null
      if(value?.v === 1) build = value.build
     } catch {/* Not a pointer */}
    }
    if(typeof build !== 'string') {
     const page = await read('entry.html')
     if(!page) return false
     build = new DOMParser().parseFromString(await page.text(), 'text/html').querySelector('meta[name="synopkgland-build"]')?.getAttribute('content')
    }
    return typeof build === 'string' && CLI_BUILD.test(build) && build !== options.build
   } catch {return false}
  }},
 }
 ctx.portal = options.portalAccess ?? (options.portal ? createPortalAccess({appId: `SYNO.SDS.${options.packageId}.Instance`,
  defaultAlias: options.portal.defaultAlias, entry: options.portal.entry, locale, location: at,
  call, send: webApi, isAdmin: () => ctx.user?.isAdmin === true,
  launch: (app, params) => {if(host.platform().host === 'standalone-web' && !options.host) openDsm(app, params); else host.openApp(app)},
 }) : undefined)
 return ctx
}

/**
 * DSM hands a url launcher's launchParam on as the characters of the text,
 * `0=l&1=i&2=n&3=k&…`, after the App's own `link=%2Fx`. Rebuilds that text,
 * or keeps a plain launchParam, and reads it as DSM reads one: `&` and `=`
 * split it, then each key and value is URL-decoded.
 */
function dsmLaunchParam(value: string | null): Array<[string, string]> {
 if(!value || value.length > 65536) return []
 let text = value
 const chars = /(\d+)=([\s\S])(?:&|$)/y
 if(/^0=/.test(value)) {
  const out: string[] = []
  chars.lastIndex = 0
  while(chars.lastIndex < value.length) {
   const at = chars.lastIndex, match = chars.exec(value)
   if(!match || Number(match[1]) !== out.length) {out.length = 0; chars.lastIndex = at; break}
   out.push(match[2]!)
  }
  if(out.length) text = out.join('')
 }
 const pairs: Array<[string, string]> = []
 for(const part of text.split('&')) {
  if(!part) continue
  const split = part.indexOf('=')
  try {pairs.push([decodeURIComponent(split < 0 ? part : part.slice(0, split)), decodeURIComponent(split < 0 ? '' : part.slice(split + 1))])}
  catch {/* Malformed escapes are not the App's values */}
 }
 return pairs
}

/** CLI-compatible bounded launch text, with native link → route normalization.
 * A launch DSM forwarded to the entry (launchParam) adds its values; the
 * page's own query wins. */
export function entryLaunchParams(search: string): LaunchParams {
 const out: Record<string, string> = Object.create(null)
 const query = new URLSearchParams(search)
 let count = 0
 const add = (key: string, value: string) => {
  if(count >= 32 || key in out || ['launch', 'launchParam', 'className', 'instanceName', 'dsm_version', 'title'].includes(key) || !/^[A-Za-z][A-Za-z0-9_.-]{0,63}$/.test(key)) return
  if(value.length > 2048 || /[\u0000-\u001f\u007f]/.test(value)) return
  out[key] = value
  count++
 }
 query.forEach((value, key) => add(key, value))
 for(const [key, value] of dsmLaunchParam(query.get('launchParam'))) add(key, value)
 const native = nativeLaunchParams({link: out.link, intake: out.intake})
 // The native validator owns these values, including route validation.
 delete out.link; delete out.intake
 return Object.freeze({...out, ...native})
}
