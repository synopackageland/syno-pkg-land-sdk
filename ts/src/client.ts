import {encodeSecretRequest,redactSecrets} from './secret'
import {createBatch} from './batch.js'
import {clientTimeout, type CallOptions} from './context'
import type {createDsmSession} from './dsm/session.js'

export interface BatchCall {method: string; params?: Record<string, unknown>}
export type BatchResult<T = unknown> = {success: true; data: T} | {success: false; error: {code: string; message: string}}
export type Batch = <T = unknown>(calls: readonly BatchCall[], options?: CallOptions) => Promise<BatchResult<T>[]>
/** Trusted App metadata, never inferred from method names or request params.
 * Copy effect from the operation registry; classify AI and streaming explicitly. */
export interface SessionRetryOperation {effect: 'read' | 'write' | 'destructive'; kind: 'rpc' | 'ai' | 'stream'}
export interface SessionRetryOptions {
 session: ReturnType<typeof createDsmSession>
 operations: Readonly<Record<string, SessionRetryOperation>>
}
export interface ClientOptions {
 endpoint: string
 token?: () => string | undefined
 ready?: () => Promise<unknown>
 fetch?: typeof globalThis.fetch
 requestTimeout?: number
 // Opt-in: this session supplies token/ready, and only declared JSON reads
 // may refresh and retry once after a decoded session_required refusal.
 sessionRetry?: SessionRetryOptions
}

/** A same-origin JSON client. Batch results stay independent and ordered. */
export function createClient(options: ClientOptions) {
 const request = options.fetch ?? globalThis.fetch
 const session = options.sessionRetry?.session
 const token = session?.token ?? options.token
 const ready = session?.ready ?? options.ready
 const retryReads = new Set(Object.entries(options.sessionRetry?.operations ?? {})
  .filter(([, operation]) => operation?.effect === 'read' && operation.kind === 'rpc').map(([method]) => method))
 async function send(envelope: object, callOptions?: CallOptions, retryMethod?: string): Promise<unknown> {
  const credentials: string[] = []
  const controller = new AbortController()
  const canceled = {code: 'request_canceled', message: 'request canceled'}
  let failure: typeof canceled | undefined
  const stop = () => {failure = canceled; controller.abort()}
  const timer = setTimeout(() => {failure = {code: 'request_timeout', message: 'request timeout'}; controller.abort()}, clientTimeout(options.requestTimeout))
  const signal = callOptions?.signal
  if(signal?.aborted) {clearTimeout(timer); throw canceled}
  signal?.addEventListener('abort', stop)
  let interrupt!: () => void
  const interrupted = new Promise<never>((_resolve, reject) => {
   interrupt = () => reject(failure ?? canceled)
   controller.signal.addEventListener('abort', interrupt, {once: true})
  })
  const wait = <T>(work: Promise<T>) => Promise.race([work, interrupted])
  try {
   // Freeze params once: recovery must never replay a caller's later edits.
   const body = encodeSecretRequest(envelope,credentials)
   const requestOnly = credentials.length > 0
   if(ready && !token?.()) await wait(ready())
   for(let attempt = 0; attempt < 2; attempt++) {
    if(failure) throw failure
    const sentToken = token?.()
    if(sentToken)credentials.push(sentToken)
    const response = await wait(request(options.endpoint, {method: 'POST', mode: 'same-origin', credentials: 'same-origin', redirect: 'error', cache: 'no-store', headers: {'Content-Type': 'application/json', ...(sentToken ? {'X-SYNO-TOKEN': sentToken} : {})}, body, signal: controller.signal}))
    const value: unknown = await wait(response.json())
    if(response.ok === false || !value || typeof value !== 'object' || !('success' in value)) throw {code: 'bad_response', message: 'unexpected response'}
    const reply = value as {success: boolean; data?: unknown; error?: {code?: unknown}}
    if(reply.success === true) return reply.data
    const error = reply.error ?? {code: 'bad_response', message: 'unexpected response'}
    if(requestOnly || reply.success !== false || error.code !== 'session_required' || attempt !== 0 || !session || !retryMethod || !retryReads.has(retryMethod)) throw error
    let fresh: string | undefined
    try {fresh = await wait(session.refresh(sentToken))} catch {throw failure ?? error}
    if(failure) throw failure
    if(!fresh || fresh === sentToken) throw error
   }
  } catch(error) {
   if(failure)throw failure
   if(error && typeof error==='object' && 'code' in error)throw redactSecrets(error,credentials)
   throw {code: 'network', message: 'request failed'}
  }
  finally {
   clearTimeout(timer)
   signal?.removeEventListener('abort', stop)
   controller.signal.removeEventListener('abort', interrupt)
  }
 }
 return {call: <T = unknown>(method: string, params?: Record<string, unknown>, callOptions?: CallOptions) => send({method, params: params ?? {}}, callOptions, method) as Promise<T>, batch: createBatch(send) as Batch}
}
