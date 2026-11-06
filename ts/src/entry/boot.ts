import type {AppStatus, Context} from '../context'
import {createClient} from '../client'
import {nativeBridge} from '../mobile/index'
import {dsmLaunchURL} from '../launch'
import {uiLocale} from '../i18n'
import {createSignInCache} from './cache'
import {createAppContext, entryLaunchParams, entryURLs, type AppContextOptions} from './context'
import {fetchEntryToken} from './token'

export interface EntryPageOptions extends AppContextOptions {
 root: HTMLElement
 /** Same-origin module URL to preload without execution while identity is pending. */
 appBundle?: string
 mount(root: HTMLElement, ctx: Context): unknown | Promise<unknown>
 /** Override navigation for an embedding host. */
 navigate?: (url: string) => void
}
type Account = Pick<AppStatus, 'user' | 'admin' | 'lang' | 'locale'>
const isAccount = (value: unknown): value is Account => {
 const v = value as Partial<Account> | null
 return !!v && typeof v.user === 'string' && !!v.user && typeof v.admin === 'boolean' &&
  (v.lang === undefined || typeof v.lang === 'string') && (v.locale === undefined || typeof v.locale === 'string')
}

/** Cookie-backed entry boot; a cache miss reads status once and never reloads DSM. */
export async function bootEntryPage(options: EntryPageOptions): Promise<Context | undefined> {
 const at = new URL(options.entryURL ?? location.href)
 const request = options.fetch ?? globalThis.fetch
 const urls = entryURLs(options.packageId, at.href)
 const navigate = options.navigate ?? (url => location.replace(url))
 const fallback = () => {navigate(dsmLaunchURL(at.origin, options.packageId, entryLaunchParams(at.search))); return undefined}
 if(options.portal && !/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(options.portal.defaultAlias)) throw new Error('Invalid portal alias')
 if(options.appBundle !== undefined) {
  let bundle: URL
  try {bundle = new URL(options.appBundle, at)} catch {throw new Error('Invalid App bundle URL')}
  if(!/^https?:$/.test(bundle.protocol) || bundle.origin !== at.origin || bundle.username || bundle.password) throw new Error('Invalid App bundle URL')
  // modulepreload fetches (and may parse dependencies) but never evaluates the
  // App. Only the authenticated mount below may import and execute it.
  const preload = document.createElement('link')
  preload.rel = 'modulepreload'; preload.href = bundle.href; preload.crossOrigin = 'anonymous'
  document.head.append(preload)
 }
 let token: string | undefined
 try {token = options.session ? await options.session.ready() : options.token ?? await fetchEntryToken(urls.token, request)}
 catch {return fallback()}
 if(!token) return fallback()
 const cache = createSignInCache(options.packageId)
 let account = cache.get('entry', token, isAccount)
 if(!account) {
  const client = createClient({endpoint: urls.cgi, requestTimeout: options.requestTimeout, fetch: request, token: () => token})
  try {
   const status = await client.call<AppStatus>('app.status')
   if(!isAccount(status)) return fallback()
   account = {user: status.user, admin: status.admin, lang: status.lang, locale: status.locale}
   cache.set('entry', token, account)
  } catch {return fallback()}
 }
 const locale = account.locale || uiLocale(account.lang || globalThis.navigator?.language || 'en')
 const ctx = createAppContext({...options, token, locale, lang: account.lang, user: {name: account.user, isAdmin: account.admin}})
 let active = true
 const disposeHost = ctx.host.dispose.bind(ctx.host)
 ctx.host.dispose = () => {active = false; disposeHost()}
 // A changed alias must not strand a reload on a 404. Probe before changing
 // history; package/WebAPI URLs above remain anchored to the original entry.
 async function landing() {
  if(!options.portal) return
  const prefix = new URL(urls.base).pathname
  const landing = new URL(prefix !== '/' ? prefix : `/${options.portal.defaultAlias}/`, at.origin)
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
