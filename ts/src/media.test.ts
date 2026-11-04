import {afterEach, describe, expect, it, vi} from 'vitest'
import {mediaUrl, type MediaGrant} from './media'
import type {Context} from './context'
import {redactSecrets} from './secret'

const id = 'a'.repeat(64)
const grant = (): MediaGrant => ({media_grant:id, url:`/cgi/api.cgi?media_grant=${id}&method=media.content`, expires_at:new Date(Date.now()+300000).toISOString(), ttl_ms:300000})
function fixture() {
 const call = vi.fn(async (method: string) => method.endsWith('.revoke') ? {revoked:true} : {...grant(),url:grant().url.replace('method=media.content',`method=${method.replace(/\.(issue|renew)$/,'.content')}`)})
 const ctx = {call:call as Context['call']}
 return {call,ctx}
}
afterEach(() => {vi.useRealTimers()})
describe('native media grants', () => {
 it('issues and renews the same URL without touching the element', async () => {
  vi.useFakeTimers()
  const {ctx,call} = fixture()
  const media = await mediaUrl(ctx,{asset:'recording'})
  const element = {src:media.url,currentTime:100,paused:false,playbackRate:1.5}
  const before = {...element}
  const firstExpiry = media.expiresAt
  await vi.advanceTimersByTimeAsync(150000)
  expect(call.mock.calls.map(c=>c[0])).toEqual(['media.issue','media.renew'])
  expect(media.url).toBe(before.src)
  expect(media.expiresAt).not.toBe(firstExpiry)
  expect(element).toEqual(before)
  expect(call).toHaveBeenLastCalledWith('media.renew',{media_grant:id},expect.objectContaining({signal:expect.any(AbortSignal)}))
  media.dispose()
  await vi.advanceTimersByTimeAsync(600000)
  expect(call).toHaveBeenCalledTimes(2)
 })
 it('stops after a renewal failure and exposes it without automatic retries', async () => {
  vi.useFakeTimers()
  const {ctx,call} = fixture(), onError = vi.fn()
  const media = await mediaUrl(ctx,{asset:'recording',onError})
  const refusal = {code:'permission_denied',message:'owner changed'}
  call.mockRejectedValueOnce(refusal)
  await vi.advanceTimersByTimeAsync(150000)
  expect(onError).toHaveBeenCalledWith(refusal)
  expect(media.error).toBe(refusal)
  await vi.advanceTimersByTimeAsync(600000)
  expect(call).toHaveBeenCalledTimes(2)
  await media.renew()
  expect(media.error).toBeUndefined()
  media.dispose()
 })
 it('revokes through authenticated RPC and aborts pending renewal work', async () => {
  vi.useFakeTimers()
  const {ctx,call} = fixture()
  const media = await mediaUrl(ctx,{asset:'recording',prefix:'record.media'})
  await media.revoke()
  expect(call).toHaveBeenLastCalledWith('record.media.revoke',{media_grant:id})
  expect((call.mock.calls[0] as unknown as [string,unknown,{signal:AbortSignal}])[2].signal.aborted).toBe(true)
  await expect(media.renew()).rejects.toMatchObject({code:'request_canceled'})
 })
 it('does not authenticate through a media URL or accept token-bearing URLs', async () => {
  const {ctx,call} = fixture()
  for(const url of [`https://other.example/media?media_grant=${id}`,`//other.example/path`,`/media?media_grant=${id}&method=media.content&SynoToken=secret`]) {
   call.mockResolvedValueOnce({...grant(),url})
   await expect(mediaUrl(ctx,{asset:'recording'})).rejects.toMatchObject({code:'bad_response'})
  }
 })
 it('does not schedule or deliver late results after disposal', async () => {
  vi.useFakeTimers()
  const {ctx,call} = fixture(), onError = vi.fn()
  const controller = new AbortController()
  const media = await mediaUrl(ctx,{asset:'recording',signal:controller.signal,onError})
  controller.abort()
  await vi.advanceTimersByTimeAsync(600000)
  expect(call).toHaveBeenCalledTimes(1)
  expect(onError).not.toHaveBeenCalled()
  await expect(media.renew()).rejects.toMatchObject({code:'request_canceled'})
 })
 it('leaves client results intact and redacts only the exact grant key in logs', async () => {
  const {ctx} = fixture()
  const media = await mediaUrl(ctx,{asset:'recording'})
  expect(media.media_grant).toBe(id)
  expect(redactSecrets({media_grant:id,grant:'ordinary',url:media.url,'Set-Cookie':'private'})).toEqual({media_grant:'[redacted]',grant:'ordinary',url:media.url.replace(id,'[redacted]'),'Set-Cookie':'[redacted]'})
  media.dispose()
 })
})
