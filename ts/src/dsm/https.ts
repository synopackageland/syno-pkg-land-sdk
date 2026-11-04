import type {Context} from '../context'
import {readDsmPorts} from './service-ports'

/** Optional link for an App's https_required page. PortInfo is administrator
 * only: unknown and ordinary subjects never start discovery. The result opens
 * DSM, not an App route; no query, fragment or credentials are carried across.
 * No result means show instructions, never guess a configured port. */
export async function readDsmHttpsURL(
 ctx: Pick<Context, 'user' | 'files' | 'webApi' | 'requestTimeout'>,
 pageURL: string,
 signal?: AbortSignal,
): Promise<string | undefined> {
 if(ctx.user?.isAdmin !== true) return undefined
 let url: URL
 try {url = new URL(pageURL)} catch {return undefined}
 if(!['http:', 'https:'].includes(url.protocol) || url.username || url.password) return undefined
 const declarations = (await readDsmPorts(ctx, signal)).filter(port =>
  port.id === 'dms_https' && /(^|[,\s])tcp($|[,\s])|^all$/.test(port.protocol))
 const ports = [...new Set(declarations.flatMap(port => port.ports))]
 if(signal?.aborted || ports.length !== 1 || declarations.some(port => port.ranges.some(range => range.first !== range.last))) return undefined
 url.protocol = 'https:'
 url.port = String(ports[0])
 url.pathname = '/webman/'
 url.search = ''
 url.hash = ''
 return url.href
}
