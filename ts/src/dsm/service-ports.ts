import {clientTimeout,type Context} from '../context'

/** One inclusive destination range; a single port has equal bounds. */
export interface DsmPortRange {first: number; last: number}
/** A localized DSM port definition. id is the services.d section (port_id).
 * ports contains single ports; ranges also retains declared intervals.
 * Neither means the service is listening or reachable. */
export interface DsmPort {
 id: string
 name: string
 desc: string
 protocol: string
 ports: number[]
 ranges: DsmPortRange[]
}

/** Best-effort PortInfo in the current administrator's DSM session. Start this
 * after rendering the App: no root helper, polling or startup dependency.
 * Uses the window's WebAPI channel when present, otherwise same-origin fetch
 * with the current files token. Missing session, refusal, timeout and malformed
 * replies return []. Names already use the session language. */
export async function readDsmPorts(ctx: Pick<Context, 'files'|'webApi'|'requestTimeout'>, signal?: AbortSignal): Promise<DsmPort[]> {
 let timer: ReturnType<typeof setTimeout>|undefined
 const controller = new AbortController()
 const abort=()=>controller.abort()
 if(signal?.aborted)return []
 signal?.addEventListener('abort',abort,{once:true})
 let stopped=()=>{}
 try {
  const request = async (): Promise<unknown> => {
   if (ctx.webApi) return ctx.webApi('SYNO.Core.Service.PortInfo', 'load', 1, {})
   const token = ctx.files?.token?.()
   if (!token) return undefined
   const response = await fetch('/webapi/entry.cgi', {
    method: 'POST', credentials: 'same-origin', mode: 'same-origin', redirect: 'error', cache: 'no-store',
    signal: controller.signal,
    headers: {'Content-Type': 'application/x-www-form-urlencoded', 'X-SYNO-TOKEN': token},
    // DSM refuses a SynoToken in the body (119; 7.1.1 and 7.4.1 alike); the header carries it.
    body: new URLSearchParams({api: 'SYNO.Core.Service.PortInfo', version: '1', method: 'load'}),
   })
   const reply = response.ok ? await response.json() : undefined
   return reply?.success === true ? reply.data : undefined
  }
  const data = await Promise.race([request(), new Promise<undefined>(resolve => {
   stopped=()=>resolve(undefined)
   controller.signal.addEventListener('abort',stopped,{once:true})
   if(controller.signal.aborted)stopped()
   timer = setTimeout(() => {controller.abort(); resolve(undefined)}, clientTimeout(ctx.requestTimeout))
  })]) as {port_info?: unknown}|null|undefined
  if (!Array.isArray(data?.port_info)) return []
  const text = (v: unknown) => typeof v === 'string' ? v.trim() : ''
  return data.port_info.flatMap((p: unknown): DsmPort[] => {
   if (!p || typeof p !== 'object') return []
   const row = p as Record<string, unknown>
   const id = text(row.port_id), protocol = text(row.protocol)
   if (!id || !Array.isArray(row.dst_port)) return []
   const ranges = row.dst_port.flatMap((value: unknown): DsmPortRange[] => {
    const match = /^(\d{1,5})(?:[-:](\d{1,5}))?$/.exec(text(value))
    if (!match) return []
    const first = Number(match[1]), last = Number(match[2] ?? match[1])
    return first >= 1 && last <= 65535 && last >= first ? [{first, last}] : []
   })
   return [{id, name: text(row.name), desc: text(row.desc), protocol,
    ports: row.dst_port.map(text).filter(v => /^\d{1,5}$/.test(v)).map(Number).filter(n => n >= 1 && n <= 65535), ranges}]
  })
 } catch {return []}
 finally {clearTimeout(timer);signal?.removeEventListener('abort',abort);controller.signal.removeEventListener('abort',stopped)}
}
