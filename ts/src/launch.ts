import type {Context, LaunchParams} from './context'
import {dsmAppTarget,type DsmAppTarget} from './dsm/app-launch'

/** The longest route launchRoute returns. */
export const MAX_LAUNCH_ROUTE = 2048

/**
 * The App route in launch params' `route`: a path from the App root, the part
 * a portal link puts after its `#`, such as `/a/notes/42`. One leading `/`, no
 * backslash or control character, at most MAX_LAUNCH_ROUTE characters; any
 * other value gives undefined. The same holds once percent-decoded, so
 * `/%2Fevil.example` or `/%5Cevil` (a URL that leaves the origin once a
 * router or a browser decodes it) gives undefined too. Hand it to the App's
 * own router only.
 */
export function launchRoute(params?: LaunchParams): string | undefined {
 const route = params?.route
 if(typeof route !== 'string' || route.length > MAX_LAUNCH_ROUTE || !appPath(route)) return undefined
 // Decode until stable (at most a few rounds): a double-encoded `%252F` is as
 // unsafe as `%2F`. A malformed escape is literal text, checked as it is.
 let text = route
 for(let round = 0; round < 3 && text.includes('%'); round++) {
  let decoded: string
  try {decoded = decodeURIComponent(text)} catch {break}
  if(decoded === text) break
  if(!appPath(decoded)) return undefined
  text = decoded
 }
 return route
}
const appPath = (route: string) => route.startsWith('/') && !route.startsWith('//') && !/[\\\u0000-\u001f\u007f]/.test(route)

/**
 * Runs navigate with the route of the launch that opened the DSM window, then
 * with that of each later launch of the open window. Returns a function that
 * stops following. A context without launch support follows nothing.
 */
export function followLaunchRoute(ctx: Partial<Pick<Context, 'launchParams' | 'onLaunch'>>, navigate: (route: string) => void): () => void {
 const first = launchRoute(ctx.launchParams)
 if(first !== undefined) navigate(first)
 return ctx.onLaunch?.(params => {
  const route = launchRoute(params)
  if(route !== undefined) navigate(route)
 }) ?? (() => {})
}

const PACKAGE_ID = /^[A-Za-z][A-Za-z0-9]{1,63}$/

/**
 * A DSM desktop link that opens the package's window with params, as
 * `<origin>/?launchApp=SYNO.SDS.<package>.Instance&launchParam=…`. DSM
 * url-decodes launchParam, splits it at `&` and `=`, then url-decodes each
 * key and value, so each is encoded twice here.
 */
export function dsmLaunchURL(origin: string, packageId: string, params: LaunchParams): string {
 if(!PACKAGE_ID.test(packageId)) throw new Error(`dsmLaunchURL: ${JSON.stringify(packageId)} is not a DSM package id`)
 const url = new URL('/', origin)
 url.searchParams.set('launchApp', `SYNO.SDS.${packageId}.Instance`)
 url.searchParams.set('launchParam', dsmLaunchParamText(params))
 return url.href
}

/** DSM's launchParam text for params: `key=value` pairs joined by `&`, each
 * key and value URL-encoded (the URL's query encodes the text once more). */
export function dsmLaunchParamText(params: LaunchParams): string {
 return Object.entries(params).map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`).join('&')
}

/** A link to a verified application ID on an explicitly known DSM origin.
 * Never infer this origin from an Application Portal or an iframe's parent. */
export function dsmAppLaunchURL(dsmOrigin:string,target:DsmAppTarget):string {
 if(typeof dsmOrigin !== 'string' || !/^https?:\/\/[^/?#]+\/?$/i.test(dsmOrigin) ||
  /[\\\s\u0000-\u001f\u007f-\u009f]/.test(dsmOrigin)) throw new Error('Invalid DSM origin')
 let origin:URL
 try {origin=new URL(dsmOrigin)} catch {throw new Error('Invalid DSM origin')}
 if(!['https:','http:'].includes(origin.protocol) || origin.username || origin.password ||
  origin.pathname !== '/' || origin.search || origin.hash) throw new Error('Invalid DSM origin')
 const checked=dsmAppTarget(target)
 const url=new URL('/',origin)
 url.searchParams.set('launchApp',checked.appId)
 url.searchParams.set('launchParam',checked.encoded)
 return url.href
}

/** DSM's launch link for Package Center, relative to the DSM origin. */
const PACKAGE_CENTER = '/?launchApp=SYNO.SDS.PkgManApp.Instance'

/**
 * The DSM desktop link that opens Package Center, where an administrator
 * installs a package. Relative (`/?launchApp=SYNO.SDS.PkgManApp.Instance`)
 * without an origin, else on that origin. It opens Package Center only: DSM
 * documents no link to a single package's page.
 */
export function packageCenterUrl(origin?: string): string {
 return origin === undefined ? PACKAGE_CENTER : new URL(PACKAGE_CENTER, origin).href
}
