import {clientTimeout} from '../context'

const COMPOUND = JSON.stringify([
 {api: 'SYNO.Core.UserSettings', method: 'get', version: 1},
 {api: 'SYNO.Core.Region.Language', method: 'get', version: 1},
])
type Result = {api?: unknown; success?: unknown; data?: unknown}
const code = (value: unknown) => typeof value === 'string' && /^[a-z]{3}$/.test(value) ? value : undefined

/** The signed-in user's DSM display language (cht, enu, …), read on DSM's own
 * origin with the page's session in one compound request: the personal
 * language, else the system language when the personal one is "def" (users
 * outside administrators cannot read it). '' means DSM follows the browser;
 * undefined means the personal language could not be read. Display
 * information for app.status's dsmLanguage hint, never an authorization. */
export async function readDsmLanguage(request: typeof globalThis.fetch, webApi: string, token: string, requestTimeout?: number): Promise<string | undefined> {
 const abort = new AbortController()
 const timer = setTimeout(() => abort.abort(), clientTimeout(requestTimeout))
 try {
  const response = await request(webApi, {method: 'POST', mode: 'same-origin', credentials: 'same-origin', redirect: 'error', cache: 'no-store', signal: abort.signal,
   headers: {'Content-Type': 'application/x-www-form-urlencoded', 'X-SYNO-TOKEN': token},
   body: new URLSearchParams({api: 'SYNO.Entry.Request', method: 'request', version: '1', stop_when_error: 'false', compound: COMPOUND})})
  const reply = response.ok ? await response.json() as {success?: unknown; data?: {result?: unknown}} : undefined
  const results: Result[] = reply?.success === true && Array.isArray(reply.data?.result) ? reply.data.result : []
  const found = (api: string) => results.find(result => result?.api === api && result.success === true)?.data as Record<string, unknown> | undefined
  const settings = found('SYNO.Core.UserSettings')
  if(!settings) return undefined
  const personal = code((settings.Personal as {lang?: unknown} | undefined)?.lang)
  if(personal && personal !== 'def') return personal
  const system = code(found('SYNO.Core.Region.Language')?.language)
  return system && system !== 'def' ? system : ''
 } catch {return undefined}
 finally {clearTimeout(timer)}
}
