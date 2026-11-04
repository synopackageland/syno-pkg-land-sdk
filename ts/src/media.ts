import type {Context} from './context'

export interface MediaGrant {
 media_grant: string
 url: string
 expires_at: string
 ttl_ms: number
}
export interface MediaOptions {
 asset: string
 /** App permission prefix, matching the Go media.Config; defaults to media. */
 prefix?: string
 signal?: AbortSignal
 /** Renewal stops on failure. The App can show the error and explicitly renew. */
 onError?: (error: unknown) => void
}
export interface MediaURL {
 /** Stable across renewals: assign to audio/video.src once. */
 readonly url: string
 readonly media_grant: string
 readonly expiresAt: string
 readonly error: unknown
 renew(): Promise<void>
 revoke(): Promise<void>
 /** Stops renewal; does not revoke the grant or unload the media element. */
 dispose(): void
}

/** Obtain an owner-checked native media URL and renew without replacing src.
 * Uses the normal authenticated ctx.call transport. No token enters the URL.
 * Dispose on unmount; pause/unload the element yourself to release bytes. */
export async function mediaUrl(ctx: Pick<Context, 'call'>, options: MediaOptions): Promise<MediaURL> {
 const prefix = options.prefix ?? 'media'
 const controller = new AbortController()
 const canceled = () => ({code: 'request_canceled', message: 'media renewal disposed'})
 let disposed = false, timer: ReturnType<typeof setTimeout> | undefined
 let failure: unknown
 let pending: Promise<void> | undefined
 const dispose = () => {
  disposed = true
  clearTimeout(timer)
  controller.abort()
  options.signal?.removeEventListener('abort', dispose)
 }
 options.signal?.addEventListener('abort', dispose, {once: true})
 if(options.signal?.aborted) dispose()
 function validate(grant: MediaGrant) {
  if(!grant || !/^[a-f0-9]{64}$/.test(grant.media_grant) || typeof grant.url !== 'string' || !grant.url.startsWith('/') || grant.url.startsWith('//') || grant.url.includes('\\')) throw {code:'bad_response',message:'invalid media grant'}
  const u = new URL(grant.url, 'https://media.invalid')
  if(u.hash || u.username || u.password || u.searchParams.size !== 2 || u.searchParams.get('method') !== `${prefix}.content` || u.searchParams.get('media_grant') !== grant.media_grant || !Number.isFinite(grant.ttl_ms) || grant.ttl_ms <= 0 || grant.ttl_ms / 2 > 0x7fffffff || !Number.isFinite(Date.parse(grant.expires_at))) throw {code:'bad_response',message:'invalid media grant'}
 }
 let grant: MediaGrant
 try {
  if(disposed) throw canceled()
  grant = await ctx.call<MediaGrant>(`${prefix}.issue`, {asset:options.asset}, {signal:controller.signal})
  validate(grant)
  if(disposed) throw canceled()
 } catch(error) {dispose();throw error}
 function schedule() {
  clearTimeout(timer)
  if(!disposed) timer = setTimeout(() => {
   void renew().catch(error => {
    if(disposed) return
    failure = error
    try {options.onError?.(error)} catch { /* App callbacks cannot create unhandled renewal errors. */ }
   })
  }, grant.ttl_ms / 2)
 }
 function renew(): Promise<void> {
  if(disposed) return Promise.reject(canceled())
  if(pending) return pending
  clearTimeout(timer)
  pending = (async () => {
   const next = await ctx.call<MediaGrant>(`${prefix}.renew`, {media_grant:grant.media_grant}, {signal:controller.signal})
   validate(next)
   if(disposed) throw canceled()
   if(next.url !== grant.url || next.media_grant !== grant.media_grant) throw {code:'bad_response',message:'media renewal changed the URL'}
   grant = next
   failure = undefined
   schedule()
  })().catch(error => {failure=error;throw error}).finally(() => {pending=undefined})
  return pending
 }
 schedule()
 return {
  get url() {return grant.url},
  get media_grant() {return grant.media_grant},
  get expiresAt() {return grant.expires_at},
  get error() {return failure},
  renew,
  async revoke() {dispose();await ctx.call(`${prefix}.revoke`, {media_grant:grant.media_grant})},
  dispose,
 }
}
