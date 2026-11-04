import type {Context} from '../context'
import {readDsmPorts} from './service-ports'

export interface DsmHttpsURLOptions {
 signal?: AbortSignal
 /** Carry launchApp and its launchParam to the HTTPS DSM desktop. */
 keepLaunchApp?: boolean
}

/** Optional link for an App's https_required page. PortInfo is administrator
 * only: unknown and ordinary subjects never start discovery. By default the
 * result opens DSM without a route; launch parameters can be kept explicitly.
 * No result means show instructions, never guess a configured port. */
export async function readDsmHttpsURL(
 ctx: Pick<Context, 'user' | 'files' | 'webApi' | 'requestTimeout'>,
 pageURL: string,
 signalOrOptions?: AbortSignal | DsmHttpsURLOptions,
): Promise<string | undefined> {
 if(ctx.user?.isAdmin !== true) return undefined
 const options = signalOrOptions && 'aborted' in signalOrOptions ? {signal: signalOrOptions} : signalOrOptions
 const signal = options?.signal
 let url: URL
 try {url = new URL(pageURL)} catch {return undefined}
 if(!['http:', 'https:'].includes(url.protocol) || url.username || url.password) return undefined
 const declarations = (await readDsmPorts(ctx, signal)).filter(port =>
  port.id === 'dms_https' && /(^|[,\s])tcp($|[,\s])|^all$/.test(port.protocol))
 const ports = [...new Set(declarations.flatMap(port => port.ports))]
 if(signal?.aborted || ports.length !== 1 || declarations.some(port => port.ranges.some(range => range.first !== range.last))) return undefined
 const launchApp = options?.keepLaunchApp ? url.searchParams.get('launchApp') : null
 const launchParam = launchApp ? url.searchParams.get('launchParam') : null
 url.protocol = 'https:'
 url.port = String(ports[0])
 url.pathname = '/webman/'
 url.search = ''
 url.hash = ''
 if(launchApp) {
  url.searchParams.set('launchApp', launchApp)
  if(launchParam !== null) url.searchParams.set('launchParam', launchParam)
 }
 return url.href
}
