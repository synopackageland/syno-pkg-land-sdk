// @vitest-environment jsdom
import {afterEach, beforeEach, expect, test, vi} from 'vitest'
import {createSignInCache} from './cache'
import {fetchEntryToken, TOKEN_HEDGE_DELAY_MS} from './token'
beforeEach(() => localStorage.clear())
afterEach(() => {vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks()})

test('slow token is hedged after four seconds; the second answer wins without any abort', async () => {
 vi.useFakeTimers()
 let original!: (response: Response) => void
 const request = vi.fn<typeof globalThis.fetch>()
  .mockImplementationOnce(() => new Promise(resolve => {original = resolve}))
  .mockResolvedValueOnce(new Response(JSON.stringify({success: true, SynoToken: 'second'})))
 const result = fetchEntryToken('/webman/login.cgi', request)
 let finished = false
 void result.then(() => {finished = true})
 await vi.advanceTimersByTimeAsync(TOKEN_HEDGE_DELAY_MS - 1)
 expect(request).toHaveBeenCalledTimes(1)
 expect(finished).toBe(false)
 await vi.advanceTimersByTimeAsync(1)
 expect(await result).toBe('second')
 expect(request).toHaveBeenCalledTimes(2)
 for(const [, init] of request.mock.calls) {
  expect(init).not.toHaveProperty('signal')
  expect(init).toMatchObject({credentials: 'same-origin', mode: 'same-origin', redirect: 'error', cache: 'no-store'})
 }
 original(new Response(JSON.stringify({success: true, SynoToken: 'late'})))
 await Promise.resolve()
 expect(await result).toBe('second')
 expect(vi.getTimerCount()).toBe(0)
})

test('both slow token requests keep waiting and the original can win after the hedge', async () => {
 vi.useFakeTimers()
 const replies: ((r: Response) => void)[] = []
 const request = vi.fn<typeof globalThis.fetch>(() => new Promise(resolve => replies.push(resolve)))
 const result = fetchEntryToken('/webman/login.cgi', request)
 let finished = false
 void result.then(() => {finished = true})
 await vi.advanceTimersByTimeAsync(60000)
 expect(finished).toBe(false)
 expect(request).toHaveBeenCalledTimes(2)
 replies[0]!(new Response(JSON.stringify({success: true, SynoToken: 'first'})))
 expect(await result).toBe('first')
 replies[1]!(new Response(JSON.stringify({success: true, SynoToken: 'second'})))
})

test('a definite token failure settles without a timed retry', async () => {
 vi.useFakeTimers()
 for(const response of [new Response('bad', {status: 403}), new Response(JSON.stringify({success: true})), new Response(JSON.stringify({success: true, SynoToken: 'bad\n'}))]) {
  const request = vi.fn<typeof globalThis.fetch>(async () => response)
  expect(await fetchEntryToken('/token', request)).toBeUndefined()
  await vi.advanceTimersByTimeAsync(10000)
  expect(request).toHaveBeenCalledTimes(1)
 }
})

test('cache scopes accounts, sign-ins and packages; malformed/blocked storage is harmless', () => {
 const a = createSignInCache('AppA'), b = createSignInCache('AppB')
 const valid = (v: unknown): v is {label: string} => typeof (v as {label?: unknown})?.label === 'string'
 a.set('home', 'credential-one', {label: 'A'}, 'alice')
 b.set('home', 'credential-one', {label: 'B'}, 'alice')
 expect(a.get('home', 'credential-one', valid, 'alice')).toEqual({label: 'A'})
 expect(localStorage.getItem('synopkgland.AppA.home')).not.toContain('credential-one')
 expect(a.get('home', 'credential-two', valid, 'alice')).toBeUndefined()
 a.set('home', 'credential-one', {label: 'A'}, 'alice')
 expect(a.get('home', 'credential-one', valid, 'bob')).toBeUndefined()
 a.set('home', 'credential-one', {label: 'A'})
 a.clear()
 expect(b.get('home', 'credential-one', valid, 'alice')).toEqual({label: 'B'})
 localStorage.setItem('synopkgland.AppA.home', 'broken')
 expect(a.get('home', 'credential-one', valid)).toBeUndefined()
 const blocked = createSignInCache('blocked', () => {throw new Error('blocked')})
 expect(() => {blocked.set('home', 'tok', {}); blocked.clear(); blocked.remove('home')}).not.toThrow()
 expect(blocked.get('home', 'tok', valid)).toBeUndefined()
})
