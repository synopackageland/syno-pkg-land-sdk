import {expect, test, vi} from 'vitest'
import {createClient} from './client'

test.each(['session_required', 'admin_required', 'app_privilege_denied'])('entry refusal %s survives single and all-public batch transports', async code => {
 const error = {code, message: 'App entry denied'}
 const fetch = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
  const body = JSON.parse(init!.body as string)
  return {json: async () => body.calls
   ? {success: true, data: {v: 1, results: body.calls.map(() => ({success: false, error}))}}
   : {success: false, error}}
 })
 const client = createClient({endpoint: '/api.cgi', fetch: fetch as unknown as typeof globalThis.fetch})
 await expect(client.call('app.public', {admin: true, user: 'operator', permission: 'public'})).rejects.toEqual(error)
 const calls = [{method: 'app.public'}, {method: 'app.stream'}, {method: 'unknown.method'}]
 expect(await client.batch(calls)).toEqual(calls.map(() => ({success: false, error})))
 expect(fetch).toHaveBeenCalledTimes(2)
 for (const [, request] of fetch.mock.calls) {
  expect(request).toMatchObject({credentials: 'same-origin', mode: 'same-origin', redirect: 'error'})
  expect(request!.headers).not.toHaveProperty('X-User')
 }
})
