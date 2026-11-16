import type {AppStatus, Context} from '../context'
import {createClient} from '../client'
import {nativeBridge} from '../mobile/index'
import {dsmLaunchURL, launchRoute} from '../launch'
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
type Handoff = {v: 1; at: number; link?: string; intake?: string}
const handoffKey = (packageId: string) => `synopkgland.${packageId}.launch`
/** DSM's sign-in, and DSM's redirect from an alias or launch link to a url
 * launcher, drop the entry's query and hash; this tab keeps the App's own
 * route and intake for the entry that opens after it. */
function keepLaunch(packageId: string, link: string | undefined, intake: string | undefined) {
 if(!link && !intake) return
 try {globalThis.sessionStorage?.setItem(handoffKey(packageId), JSON.stringify({v: 1, at: Date.now(), ...(link ? {link} : {}), ...(intake ? {intake} : {})}))} catch {/* optional storage */}
}
function takeLaunch(packageId: string): {link?: string; intake?: string} | undefined {
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
  return link || intake ? {link, intake} : undefined
 } catch {return undefined}
}
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
 const request = options.fetch ?? globalThis.fetch
 const urls = entryURLs(options.packageId, at.href)
 const navigate = options.navigate ?? (url => location.replace(url))
 const ingress = statusEndpoint(options.packageId, at.href, options.daemonIngress)
 // Below DSM's alias the prefix is the alias; otherwise the declared default.
 const prefix = new URL(urls.base).pathname
 const alias = () => new URL(prefix !== '/' ? prefix : `/${options.portal!.defaultAlias}/`, at.origin)
 // An active alias shows the App's branded DSM sign-in and keeps the query
 // across it (DSM replaces the hash), so the route rides along as ?link=.
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
  const route = at.hash.startsWith('#') ? at.hash.slice(1) : ''
  if(!query.has('link') && launchRoute({route}) !== undefined) query.set('link', route)
  landing.search = query.toString()
  landing.hash = at.hash
  return landing.href
 }
 // A portal alias first, else DSM's App start; both keep the launch intent.
 async function fallback(): Promise<undefined> {
  const launch = entryLaunchParams(at.search)
  const route = at.hash.startsWith('#') ? at.hash.slice(1) : ''
  keepLaunch(options.packageId, launch.route ?? (launchRoute({route}) !== undefined ? route : undefined), launch.intake)
  const portal = options.portal ? await portalSignIn() : undefined
  navigate(portal ?? dsmLaunchURL(at.origin, options.packageId, entryLaunchParams(at.search)))
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
 let account = cache.get('entry', token, isAccount)
 if(!account) {
  const client = createClient({endpoint: ingress ?? urls.cgi, requestTimeout: options.requestTimeout,
   fetch: ingress ? statusFetch(request, ingress, urls.cgi) : request, token: () => token})
  account = await readAccount(client, await readDsmLanguage(request, urls.webApi, token, options.requestTimeout))
  if(!account) return fallback()
  cache.set('entry', token, account)
 }
 const locale = account.locale || uiLocale(account.lang || globalThis.navigator?.language || 'en')
 // A launch kept across a sign-in comes back when this page carries none.
 const own = entryLaunchParams(at.search)
 const kept = takeLaunch(options.packageId)
 let launchParams = options.launchParams
 if(kept && !own.link && !own.intake && !launchParams) {
  const restored = new URL(at.href)
  if(kept.link) restored.searchParams.set('link', kept.link)
  if(kept.intake) restored.searchParams.set('intake', kept.intake)
  try {history.replaceState(history.state, '', restored.href)} catch {/* The App still gets launchParams */}
  launchParams = entryLaunchParams(restored.search)
 }
 const ctx = createAppContext({...options, ...(launchParams ? {launchParams} : {}), pageURL: at.href, token, locale, lang: account.lang, user: {name: account.user, isAdmin: account.admin}})
 let active = true
 const disposeHost = ctx.host.dispose.bind(ctx.host)
 ctx.host.dispose = () => {active = false; disposeHost()}
 // A changed alias must not strand a reload on a 404. Probe before changing
 // history; package/WebAPI URLs above remain anchored to the original entry.
 async function landing() {
  if(!options.portal || options.rewriteToAlias !== true) return
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
 try {await options.mount(options.root, ctx)} catch(error) {ctx.host.dispose(); throw error}
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
