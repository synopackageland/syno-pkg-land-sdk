import {expect, test, vi} from 'vitest'
import type {Context} from '../context'
import {createEntryCheck} from './check'

function context(call: (method: string, params?: Record<string, unknown>, options?: {signal?: AbortSignal}) => Promise<unknown>) {
 return {call} as Pick<Context, 'call'>
}

test('a fresh backend check ignores cached UI privileges and shares in-flight work', async () => {
 let resolve!: (value: unknown) => void
 const call = vi.fn(() => new Promise(r => {resolve = r}))
 const result = vi.fn()
 const check = createEntryCheck(context(call), 'admin-only', result)
 const pending = check.check()
 expect(check.check()).toBe(pending)
 expect(result).not.toHaveBeenCalled()
 resolve({user: 'test-admin', admin: true})
 await pending
 expect(result).toHaveBeenCalledWith(undefined)
 expect(call).toHaveBeenCalledTimes(1)
 expect(call.mock.calls[0]).toMatchObject(['app.status', {}, {signal: expect.any(AbortSignal)}])
 check.dispose()
})

test('ordinary users, expired sessions, entry denials and unknown results fail closed', async () => {
 for(const [response, code] of [
  [{user: 'test-user', admin: false}, 'admin_required'],
  [{user: '', admin: true}, 'auth_unavailable'],
  [{user: 'test-admin', admin: 'true'}, 'auth_unavailable'],
  [null, 'auth_unavailable'],
 ] as const) {
  const result = vi.fn()
  await createEntryCheck(context(async () => response), 'admin-only', result).check()
  expect(result).toHaveBeenCalledWith({code})
 }
 for(const code of ['session_required', 'admin_required', 'app_privilege_denied', 'request_timeout']) {
  const result = vi.fn()
  await createEntryCheck(context(async () => {throw {code}}), 'admin-only', result).check()
  expect(result).toHaveBeenCalledWith({code: code === 'request_timeout' ? 'auth_unavailable' : code})
 }
})

test('app-privilege allows a non-admin admitted by the CGI and retry can clear a failure', async () => {
 let admin = false
 const result = vi.fn()
 const ctx = context(async () => ({user: 'test-user', admin}))
 const app = createEntryCheck(ctx, 'app-privilege', result)
 await app.check()
 expect(result).toHaveBeenLastCalledWith(undefined)
 const check = createEntryCheck(ctx, 'admin-only', result)
 await check.check()
 expect(result).toHaveBeenLastCalledWith({code: 'admin_required'})
 admin = true
 await check.check()
 expect(result).toHaveBeenLastCalledWith(undefined)
})

test('dispose aborts in-flight calls and prevents late dialogs or new checks', async () => {
 let resolve!: (value: unknown) => void
 let signal: AbortSignal | undefined
 const result = vi.fn()
 const call = vi.fn((_method, _params, options) => {
  signal = options?.signal
  return new Promise(r => {resolve = r})
 })
 const check = createEntryCheck(context(call), 'admin-only', result)
 const pending = check.check()
 check.dispose()
 expect(signal?.aborted).toBe(true)
 resolve({user: 'test-user', admin: false})
 await pending
 await check.check()
 expect(result).not.toHaveBeenCalled()
 expect(call).toHaveBeenCalledTimes(1)
})
