import type {Context, LaunchParams} from './context'

/** The longest route launchRoute returns. */
export const MAX_LAUNCH_ROUTE = 2048

/**
 * The App route in launch params' `route`: a path from the App root, the part
 * a portal link puts after its `#`, such as `/a/notes/42`. One leading `/`, no
 * backslash or control character, at most MAX_LAUNCH_ROUTE characters; any
 * other value gives undefined. Hand it to the App's own router only.
 */
export function launchRoute(params?: LaunchParams): string | undefined {
 const route = params?.route
 if(typeof route !== 'string' || route.length > MAX_LAUNCH_ROUTE) return undefined
 if(!route.startsWith('/') || route.startsWith('//') || /[\\\u0000-\u001f\u007f]/.test(route)) return undefined
 return route
}

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
 url.searchParams.set('launchParam', Object.entries(params).map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`).join('&'))
 return url.href
}
