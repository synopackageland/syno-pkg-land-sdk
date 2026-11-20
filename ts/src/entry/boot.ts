import type {AppStatus, Context, LaunchParams} from '../context'
import {createClient} from '../client'
import {nativeBridge} from '../mobile/index'
import {dsmLaunchParamText, dsmLaunchURL, launchRoute} from '../launch'
import {uiLocale} from '../i18n'
import {createSignInCache} from './cache'
import {createAppContext, entryLaunchParams, entryURLs, statusEndpoint, statusFetch, type AppContextOptions} from './context'
import {fetchEntryToken} from './token'
import {readDsmLanguage} from './language'

export interface EntryPageOptions extends AppContextOptions {
 root: HTMLElement
 /** Same-origin module URL to preload without execution while identity is pending. */
 appBundle?: string
 /** Same-origin classic script URL (the App's app.js) to download without
  * execution while identity is pending; `mount` adds the script element. */
 appScript?: string
 /** More same-origin ES modules the first screen imports, such as the App's
  * main chunk, to modulepreload with the bundle. */
 preload?: readonly string[]
 mount(root: HTMLElement, ctx: Context): unknown | Promise<unknown>
 /** Override navigation for an embedding host. */
 navigate?: (url: string) => void
 /** Override the one reload that applies a DSM display language that differs
  * from a cached sign-in's, for an embedding host. */
 reload?: () => void
 /** Show the portal alias in the address bar after mount (the address a
  * reload then opens). Off by default: a reload of the alias boots DSM's
  * whole desktop before the App, the entry page does not. */
 rewriteToAlias?: boolean
}
/** Waits between app.status attempts after a failure that is not a refusal
 * (busy, request_timeout, an unreadable reply, a network error). The number
 * of attempts bounds the retries; no wall-clock budget fails a slow one,
 * and each request keeps the client's own request deadline. */
export const ENTRY_STATUS_RETRY_DELAYS_MS: readonly number[] = Object.freeze([250, 500, 1000, 2000])
/** How long a fallback waits for the portal alias to answer before it picks
 * DSM's App start instead. It chooses a sign-in page; it fails nothing. */
export const PORTAL_FALLBACK_PROBE_MS = 4000
/** The App CGI's EntryGate refusals: the session or account cannot open the
 * App, so a retry cannot succeed and DSM's own sign-in takes over. */
const REFUSALS = new Set(['session_required', 'admin_required', 'app_privilege_denied'])
/** How long a launch kept across a DSM sign-in stays usable. An expiry of a
 * stored hint, not a request deadline: an older one is dropped. */
export const LAUNCH_HANDOFF_MAX_AGE_MS = 30 * 60 * 1000
type Handoff = {v: 1; at: number; link?: string; intake?: string; short?: string}
const SHORT_PATH = /^\/[a-z0-9]+(?:-[a-z0-9]+)*$/
/** The marker DSM's short-path redirect puts first in the query of the page it
 * opens (`/<path>` → `…?spl-short=<path>&…`). Only a page reached through the
 * short path has it, so the short path is known to work on this origin. */
const MARKED = /^\?spl-short=([a-z0-9]+(?:-[a-z0-9]+)*)(?:&|$)/
/** Show `path` in the address bar. A `<base>` first keeps the App's relative
 * URLs (images, fetches, links added later) on the entry page's folder. */
function showShortPath(at: URL, path: string): boolean {
 try {
  if(!document.querySelector('base[href]')) {
   const base = document.createElement('base')
   base.href = `${at.origin}${at.pathname}`
   document.head.prepend(base)
  }
  history.replaceState(history.state, '', `${path}${at.search}${at.hash}`)
  return true
 } catch {return false}
}
/** Query names that hold a DSM session or a secret; never put in launchParam. */
const CREDENTIAL_PARAM = /^(?:synotoken|_?sid|did|token|access_token|id_token|refresh_token|password|passwd|otp_code|secret)$/i
const handoffKey = (packageId: string) => `synopkgland.${packageId}.launch`
/** DSM's sign-in, and DSM's redirect from an alias or launch link to a url
 * launcher, drop the entry's query and hash; this tab keeps the App's own
 * route and intake for the entry that opens after it. */
function keepLaunch(packageId: string, link: string | undefined, intake: string | undefined, short: string | undefined) {
 if(!link && !intake && !short) return
 try {globalThis.sessionStorage?.setItem(handoffKey(packageId), JSON.stringify({v: 1, at: Date.now(), ...(link ? {link} : {}), ...(intake ? {intake} : {}), ...(short ? {short} : {})}))} catch {/* optional storage */}
}
function takeLaunch(packageId: string): {link?: string; intake?: string; short?: string} | undefined {
 try {
  const storage = globalThis.sessionStorage
  const raw = storage?.getItem(handoffKey(packageId))
  if(!raw) return undefined
  storage!.removeItem(handoffKey(packageId))
  const value = JSON.parse(raw) as Partial<Handoff> | null
  const age = Date.now() - Number(value?.at)
  if(value?.v !== 1 || !(age >= 0 && age <= LAUNCH_HANDOFF_MAX_AGE_MS)) return undefined
  const link = typeof value.link === 'string' && launchRoute({route: value.link}) !== undefined ? value.link : undefined
  const intake = typeof value.intake === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value.intake) ? value.intake : undefined
  const short = typeof value.short === 'string' && SHORT_PATH.test(value.short) ? value.short : undefined
  return link || intake || short ? {link, intake, short} : undefined
 } catch {return undefined}
}
/** A tab reloads at most once into the same changed language. */
const relocaleKey = (packageId: string) => `synopkgland.${packageId}.relocale`
function reloadOnce(packageId: string, locale: string): boolean {
 try {
  const storage = globalThis.sessionStorage
  if(!storage || storage.getItem(relocaleKey(packageId)) === locale) return false
  storage.setItem(relocaleKey(packageId), locale)
  return true
 } catch {return false}
}
function relocaled(packageId: string) {
 try {globalThis.sessionStorage?.removeItem(relocaleKey(packageId))} catch {/* optional storage */}
}
/** Input that makes a reload lose the user's work. */
const USER_INPUT = ['pointerdown', 'keydown'] as const
type Account = Pick<AppStatus, 'user' | 'admin' | 'lang' | 'locale'>
const isAccount = (value: unknown): value is Account => {
 const v = value as Partial<Account> | null
 return !!v && typeof v.user === 'string' && !!v.user && typeof v.admin === 'boolean' &&
  (v.lang === undefined || typeof v.lang === 'string') && (v.locale === undefined || typeof v.locale === 'string')
}

/** One verified identity, or undefined when the CGI refused the caller,
 * answered without an identity, or failed every attempt. A failure that is
 * not a refusal is retried after each ENTRY_STATUS_RETRY_DELAYS_MS wait.
 * dsmLanguage, when DSM answered it, lets app.status answer in the DSM
 * display language without the backend replaying this browser's session. */
async function readAccount(client: ReturnType<typeof createClient>, dsmLanguage: string | undefined): Promise<Account | undefined> {
 for(let attempt = 0; ; attempt++) {
  let failure: unknown
  try {
   const status = await client.call<AppStatus>('app.status', dsmLanguage === undefined ? {} : {dsmLanguage})
   return isAccount(status) ? {user: status.user, admin: status.admin, lang: status.lang, locale: status.locale} : undefined
  } catch(error) {failure = error}
  const code = (failure as {code?: unknown} | null)?.code
  if(typeof code === 'string' && REFUSALS.has(code)) return undefined
  const wait = ENTRY_STATUS_RETRY_DELAYS_MS[attempt]
  if(wait === undefined) return undefined
  await new Promise(resolve => setTimeout(resolve, wait))
 }
}

/** Cookie-backed entry boot; a cache miss reads status (retrying transient
 * failures) and never reloads DSM unless the caller cannot be verified. */
export async function bootEntryPage(options: EntryPageOptions): Promise<Context | undefined> {
 const at = new URL(options.pageURL ?? location.href)
 // Opened through its short path: drop the marker and show the short path.
 // Package, CGI and WebAPI URLs stay anchored to the entry page.
 const shortPath = typeof options.shortPath === 'string' && SHORT_PATH.test(options.shortPath) ? options.shortPath : undefined
 const marked = MARKED.exec(at.search)
 let short = false
 if(marked && shortPath === `/${marked[1]}`) {
  at.search = at.search.slice(marked[0].length)
  short = showShortPath(at, shortPath)
 }
 const request = options.fetch ?? globalThis.fetch
 const urls = entryURLs(options.packageId, at.href)
 const navigate = options.navigate ?? (url => location.replace(url))
 const ingress = statusEndpoint(options.packageId, at.href, options.daemonIngress)
 // Below DSM's alias the prefix is the alias; otherwise the declared default.
 const prefix = new URL(urls.base).pathname
 const alias = () => new URL(prefix !== '/' ? prefix : `/${options.portal!.defaultAlias}/`, at.origin)
 // What the page was opened to do, for DSM to hand the App's window after a
 // sign-in: the page's launch params, with a hash route as `route` when the
 // query names none. A route launchRoute refuses is dropped, and so is any
 // value named like a credential: a sign-in address carries no session.
 function signInLaunch(): LaunchParams {
  const params: Record<string, string> = {...entryLaunchParams(at.search)}
  for(const key of Object.keys(params)) if(CREDENTIAL_PARAM.test(key)) delete params[key]
  if(params.route !== undefined && launchRoute(params) === undefined) delete params.route
  const route = at.hash.startsWith('#') ? at.hash.slice(1) : ''
  if(params.route === undefined && launchRoute({route}) !== undefined) params.route = route
  return params
 }
 // An active alias shows the App's branded DSM sign-in and keeps the query
 // across it (DSM replaces the hash). After the sign-in DSM opens the App on
 // the alias page as a DSM window, which reads DSM's launchParam only, so the
 // launch rides along as launchParam; ?link= stays for the entry page.
 async function portalSignIn(): Promise<string | undefined> {
  const landing = alias()
  let timer: ReturnType<typeof setTimeout> | undefined
  const abort = new AbortController()
  try {
   const response = await Promise.race([
    request(landing.href, {method: 'GET', mode: 'same-origin', redirect: 'error', credentials: 'same-origin', cache: 'no-store', signal: abort.signal}),
    new Promise<undefined>(resolve => {timer = setTimeout(() => {abort.abort(); resolve(undefined)}, PORTAL_FALLBACK_PROBE_MS)}),
   ])
   if(!response?.ok) return undefined
  } catch {return undefined}
  finally {clearTimeout(timer)}
  const query = new URLSearchParams(at.search)
  query.delete('launch')
  const credentials: string[] = []
  query.forEach((_value, key) => {if(CREDENTIAL_PARAM.test(key)) credentials.push(key)})
  for(const key of credentials) query.delete(key)
  if(query.has('link') && launchRoute({route: query.get('link')!}) === undefined) query.delete('link')
  const route = at.hash.startsWith('#') ? at.hash.slice(1) : ''
  if(!query.has('link') && launchRoute({route}) !== undefined) query.set('link', route)
  query.delete('launchParam')
  const launch = signInLaunch()
  if(Object.keys(launch).length) query.set('launchParam', dsmLaunchParamText(launch))
  landing.search = query.toString()
  landing.hash = launchRoute({route}) !== undefined ? at.hash : ''
  return landing.href
 }
 // A portal alias first, else DSM's App start; both keep the launch intent.
 async function fallback(): Promise<undefined> {
  const launch = entryLaunchParams(at.search)
  const route = at.hash.startsWith('#') ? at.hash.slice(1) : ''
  keepLaunch(options.packageId, launch.route ?? (launchRoute({route}) !== undefined ? route : undefined), launch.intake, short ? shortPath : undefined)
  const portal = options.portal ? await portalSignIn() : undefined
  navigate(portal ?? dsmLaunchURL(at.origin, options.packageId, signInLaunch()))
  return undefined
 }
 if(options.portal && !/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(options.portal.defaultAlias)) throw new Error('Invalid portal alias')
 // Preloading fetches (and may parse module dependencies) but never evaluates
 // the App: only the authenticated mount below may import and execute it.
 // Every URL is checked before any download starts.
 const preloads = [
  ...(options.appBundle !== undefined ? [{href: options.appBundle, module: true}] : []),
  ...(options.appScript !== undefined ? [{href: options.appScript, module: false}] : []),
  ...(options.preload ?? []).map(href => ({href, module: true})),
 ].map(({href, module}) => {
  let url: URL
  try {url = new URL(href, at)} catch {throw new Error('Invalid App bundle URL')}
  if(!/^https?:$/.test(url.protocol) || url.origin !== at.origin || url.username || url.password) throw new Error('Invalid App bundle URL')
  const link = document.createElement('link')
  // A module preload must match the import's CORS mode; a classic one the plain <script>'s.
  if(module) {link.rel = 'modulepreload'; link.crossOrigin = 'anonymous'} else {link.rel = 'preload'; link.as = 'script'}
  link.href = url.href
  return link
 })
 document.head.append(...preloads)
 let token: string | undefined
 try {token = options.session ? await options.session.ready() : options.token ?? await fetchEntryToken(urls.token, request)}
 catch {return fallback()}
 if(!token) return fallback()
 const cache = createSignInCache(options.packageId)
 const statusClient = () => createClient({endpoint: ingress ?? urls.cgi, requestTimeout: options.requestTimeout,
  fetch: ingress ? statusFetch(request, ingress, urls.cgi) : request, token: () => token})
 const dsmLanguage = () => readDsmLanguage(request, urls.webApi, token!, options.requestTimeout)
 const localeOf = (value: Account) => value.locale || uiLocale(value.lang || globalThis.navigator?.language || 'en')
 let account = cache.get('entry', token, isAccount)
 // A cached sign-in mounts without waiting; DSM's display language is still
 // read meanwhile and sent as the status hint, so a language changed in DSM,
 // or a cache written before the hint existed, does not stay in effect. An
 // unreadable language sends nothing and keeps the cached one.
 const recheck = account ? dsmLanguage().then(language => language === undefined ? undefined : readAccount(statusClient(), language)).catch(() => undefined) : undefined
 if(!account) {
  account = await readAccount(statusClient(), await dsmLanguage())
  if(!account) return fallback()
  cache.set('entry', token, account)
 }
 const locale = localeOf(account)
 // A launch kept across a sign-in comes back when this page carries none.
 const own = entryLaunchParams(at.search)
 const kept = takeLaunch(options.packageId)
 let launchParams = options.launchParams
 // The short path the page was opened with before a DSM sign-in.
 if(!short && kept?.short && kept.short === shortPath) short = showShortPath(at, shortPath)
 if(kept && (kept.link || kept.intake) && !own.link && !own.intake && !launchParams) {
  const restored = new URL(at.href)
  if(kept.link) restored.searchParams.set('link', kept.link)
  if(kept.intake) restored.searchParams.set('intake', kept.intake)
  try {history.replaceState(history.state, '', short ? `${shortPath}${restored.search}${restored.hash}` : restored.href)} catch {/* The App still gets launchParams */}
  launchParams = entryLaunchParams(restored.search)
 }
 const ctx = createAppContext({...options, ...(launchParams ? {launchParams} : {}), pageURL: at.href, token, locale, lang: account.lang, user: {name: account.user, isAdmin: account.admin}})
 let active = true
 const disposeHost = ctx.host.dispose.bind(ctx.host)
 ctx.host.dispose = () => {active = false; disposeHost()}
 // A changed alias must not strand a reload on a 404. Probe before changing
 // history; package/WebAPI URLs above remain anchored to the original entry.
 async function landing() {
  if(!options.portal || options.rewriteToAlias !== true || short) return
  const landing = alias()
  try {
   const response = await request(landing.href, {method: 'GET', mode: 'same-origin', redirect: 'error', credentials: 'same-origin', cache: 'no-store'})
   if(response.ok && active) {
    // The App may already have followed a launch route while the probe ran.
    landing.search = location.search
    landing.searchParams.delete('launch')
    landing.hash = location.hash
    history.replaceState(history.state, '', landing.href)
   }
  } catch {/* Keep the working entry URL */}
 }
 if(options.displayName) document.title = options.displayName
 options.root.lang = locale
 let touched = false
 const touch = () => {touched = true}
 const untouch = () => {for(const type of USER_INPUT) globalThis.removeEventListener?.(type, touch, true)}
 if(recheck) for(const type of USER_INPUT) globalThis.addEventListener?.(type, touch, true)
 try {await options.mount(options.root, ctx)} catch(error) {untouch(); ctx.host.dispose(); throw error}
 // A different language from DSM replaces the cached one, and the page
 // reloads once into it unless the user has started working; then the next
 // open shows it. The EntryGuard, not this display read, decides access.
 if(recheck) void recheck.then(fresh => {
  untouch()
  if(!fresh || fresh.user !== ctx.user?.name) return
  cache.set('entry', token!, fresh)
  if(localeOf(fresh) === locale) {relocaled(options.packageId); return}
  if(active && !touched && reloadOnce(options.packageId, localeOf(fresh))) (options.reload ?? (() => location.reload()))()
 })
 const bridge = nativeBridge()
 if(bridge && typeof globalThis.performance?.getEntriesByName === 'function') {
  let observer: PerformanceObserver | undefined
  const ready = (entries: readonly PerformanceEntry[]) => {
   const fcp = entries.find(entry => entry.name === 'first-contentful-paint')
   if(!fcp) return false
   observer?.disconnect()
   try {bridge.post({v: 1, type: 'ready', fcp: fcp.startTime})} catch {/* A host message cannot undo a mounted App */}
   return true
  }
  if(!ready(performance.getEntriesByName('first-contentful-paint')) && typeof PerformanceObserver !== 'undefined') {
   observer = new PerformanceObserver(list => {ready(list.getEntries())})
   try {observer.observe({type: 'paint', buffered: true})} catch {observer.disconnect()}
   const dispose = ctx.host.dispose.bind(ctx.host)
   ctx.host.dispose = () => {observer?.disconnect(); dispose()}
  }
 }
 // Navigation polish must not hold the first render behind a slow portal.
 void landing()
 return ctx
}
