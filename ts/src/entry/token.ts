/** A second request after this delay, never a deadline on either request. */
export const TOKEN_HEDGE_DELAY_MS = 4000

/** First completed token answer wins. Neither a slow request nor the loser is aborted. */
export function fetchEntryToken(url: string, request: typeof globalThis.fetch = globalThis.fetch): Promise<string | undefined> {
 return new Promise(resolve => {
  let done = false
  const finish = (token?: string) => {
   if(done) return
   done = true
   clearTimeout(timer)
   resolve(token)
  }
  async function send() {
   try {
    const response = await request(url, {credentials: 'same-origin', mode: 'same-origin', redirect: 'error', cache: 'no-store'})
    const reply = response.ok ? await response.json() : undefined
    const token = reply?.success === true ? reply.SynoToken : undefined
    finish(typeof token === 'string' && token.length > 0 && token.length <= 4096 && !/[\u0000-\u001f\u007f-\u009f]/.test(token) ? token : undefined)
   } catch {finish()}
  }
  const timer = setTimeout(() => {void send()}, TOKEN_HEDGE_DELAY_MS)
  void send()
 })
}
