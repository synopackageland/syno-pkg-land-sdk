import {createBatch} from './batch.js'
import {clientTimeout, type CallOptions} from './context'

export interface BatchCall {method: string; params?: Record<string, unknown>}
export type BatchResult<T = unknown> = {success: true; data: T} | {success: false; error: {code: string; message: string}}
export type Batch = <T = unknown>(calls: readonly BatchCall[], options?: CallOptions) => Promise<BatchResult<T>[]>
export interface ClientOptions {endpoint: string; token?: () => string | undefined; ready?: () => Promise<unknown>; fetch?: typeof globalThis.fetch; requestTimeout?: number}

/** A same-origin JSON client. Batch results stay independent and ordered. */
export function createClient(options: ClientOptions) {
 const request = options.fetch ?? globalThis.fetch
 async function send(envelope: object, callOptions?: CallOptions): Promise<unknown> {
  const controller = new AbortController()
  const canceled = {code: 'request_canceled', message: 'request canceled'}
  let failure: typeof canceled | undefined
  const stop = () => {failure = canceled; controller.abort()}
  const timer = setTimeout(() => {failure = {code: 'request_timeout', message: 'request timeout'}; controller.abort()}, clientTimeout(options.requestTimeout))
  const signal = callOptions?.signal
  if(signal?.aborted) {clearTimeout(timer); throw canceled}
  signal?.addEventListener('abort', stop)
  try {
   if(options.ready && !options.token?.()) {
    await Promise.race([options.ready(), new Promise<void>(resolve => controller.signal.addEventListener('abort', () => resolve(), {once: true}))])
   }
   if(failure) throw failure
   const token = options.token?.()
   const response = await request(options.endpoint, {method: 'POST', mode: 'same-origin', credentials: 'same-origin', redirect: 'error', cache: 'no-store', headers: {'Content-Type': 'application/json', ...(token ? {'X-SYNO-TOKEN': token} : {})}, body: JSON.stringify(envelope), signal: controller.signal})
   const body: unknown = await response.json()
   if(!body || typeof body !== 'object' || !('success' in body)) throw {code: 'bad_response', message: 'unexpected response'}
   const reply = body as {success: boolean; data?: unknown; error?: unknown}
   if(!reply.success) throw reply.error ?? {code: 'bad_response', message: 'unexpected response'}
   return reply.data
  } catch(error) {throw failure ?? error}
  finally {clearTimeout(timer); signal?.removeEventListener('abort', stop)}
 }
 return {call: <T = unknown>(method: string, params?: Record<string, unknown>, callOptions?: CallOptions) => send({method, params: params ?? {}}, callOptions) as Promise<T>, batch: createBatch(send) as Batch}
}
