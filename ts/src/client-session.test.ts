import {afterEach, expect, test, vi} from 'vitest'
import {createClient, type SessionRetryOperation} from './client'
import {createDsmSession} from './dsm/session.js'

afterEach(() => vi.useRealTimers())
const refusal = {code: 'session_required', message: 'Sign in again'}
const response = (body: unknown) => ({ok: true, json: async () => body}) as Response
const operations: Record<string, SessionRetryOperation> = {
 'settings.get': {effect: 'read', kind: 'rpc'},
 'settings.set': {effect: 'write', kind: 'rpc'},
 'settings.delete': {effect: 'destructive', kind: 'rpc'},
 'assistant.ask': {effect: 'read', kind: 'ai'},
 'events.watch': {effect: 'read', kind: 'stream'},
}
function setup(error: unknown = refusal, extra: {enabled?: boolean; refreshFails?: boolean} = {}) {
 const tokens: (string | undefined)[] = []
 const fetch = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
  tokens.push((init?.headers as Record<string, string>)?.['X-SYNO-TOKEN'])
  return response(tokens.length === 1 ? {success: false, error} : {success: true, data: 'done'})
 })
 const recover = vi.fn(async () => response(extra.refreshFails ? {success: false} : {success: true, SynoToken: 'fresh'}))
 const session = createDsmSession({read: () => 'stale', fetch: recover})
 const client = createClient({endpoint: '/api.cgi', token: session.token, fetch,
  ...(extra.enabled === false ? {} : {sessionRetry: {session, operations}})})
 return {client, fetch, recover, tokens}
}

test('an opted-in read refreshes the stale carrier and replays the same body exactly once', async () => {
 const {client, fetch, recover, tokens} = setup()
 expect(await client.call('settings.get', {section: 'display'})).toBe('done')
 expect(tokens).toEqual(['stale', 'fresh'])
 expect(recover).toHaveBeenCalledOnce()
 expect(fetch.mock.calls[0]![1]!.body).toBe(fetch.mock.calls[1]![1]!.body)
 for(const [, init] of fetch.mock.calls) expect(init).toMatchObject({mode: 'same-origin', credentials: 'same-origin', redirect: 'error'})
})

test('concurrent reads share a refresh, and a late stale refusal uses the recovered token', async () => {
 let complete!:(reply: Response) => void
 let late!:(reply: Response) => void
 const recover = vi.fn(() => new Promise<Response>(resolve => {complete = resolve}))
 const session = createDsmSession({read: () => 'stale', fetch: recover})
 let staleCalls = 0
 const fetch = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
  if((init?.headers as Record<string,string>)['X-SYNO-TOKEN'] === 'fresh') return response({success:true, data:'done'})
  staleCalls++
  if(staleCalls === 3) return new Promise<Response>(resolve => {late = resolve})
  return response({success:false, error:refusal})
 })
 const client = createClient({endpoint:'/api.cgi', fetch, sessionRetry:{session, operations}})
 const first = client.call('settings.get'), second = client.call('settings.get'), third = client.call('settings.get')
 await vi.waitFor(() => expect(recover).toHaveBeenCalledOnce())
 complete(response({success:true, SynoToken:'fresh'}))
 expect(await first).toBe('done')
 expect(await second).toBe('done')
 late(response({success:false, error:refusal}))
 expect(await third).toBe('done')
 expect(recover).toHaveBeenCalledOnce()
 expect(fetch).toHaveBeenCalledTimes(6)
})

test('request params and retry policy are snapshots, not mutable caller hints', async () => {
 const {client, fetch, recover} = setup()
 const params = {section:'original'}
 const pending = client.call('settings.get', params)
 params.section = 'changed'
 expect(await pending).toBe('done')
 expect(fetch.mock.calls.map(([, init]) => JSON.parse(init!.body as string).params)).toEqual([
  {section:'original'}, {section:'original'},
 ])
 expect(recover).toHaveBeenCalledOnce()
 const policy = {'settings.set': {effect:'write', kind:'rpc'}} as const
 const guarded = createClient({endpoint:'/api.cgi', fetch, sessionRetry:{session:createDsmSession({read:()=> 'stale',fetch:recover}), operations:policy}})
 Object.assign(policy['settings.set'], {effect:'read'})
 fetch.mockImplementation(async () => response({success:false,error:refusal}))
 await expect(guarded.call('settings.set', {effect:'read',kind:'rpc'})).rejects.toEqual(refusal)
 expect(recover).toHaveBeenCalledOnce()
})

test.each(['settings.set', 'settings.delete', 'assistant.ask', 'events.watch', 'unknown.get', 'toString'])(
 '%s never refreshes or replays', async method => {
  const {client, fetch, recover} = setup()
  await expect(client.call(method)).rejects.toEqual(refusal)
  expect(fetch).toHaveBeenCalledOnce()
  expect(recover).not.toHaveBeenCalled()
 })

test.each(['https_required', 'admin_required', 'app_privilege_denied', 'request_timeout', 'NOT_AUTHENTICATED'])(
 '%s does not trigger session recovery', async code => {
  const error = {code, message: 'Refused'}
  const {client, fetch, recover} = setup(error)
  await expect(client.call('settings.get')).rejects.toEqual(error)
  expect(fetch).toHaveBeenCalledOnce()
  expect(recover).not.toHaveBeenCalled()
 })

test('the existing client remains opt-in and refresh failure keeps the original refusal', async () => {
 for(const extra of [{enabled: false}, {refreshFails: true}]) {
  const {client, fetch} = setup(refusal, extra)
  await expect(client.call('settings.get')).rejects.toEqual(refusal)
  expect(fetch).toHaveBeenCalledOnce()
 }
})

test('a second session refusal propagates without another refresh', async () => {
 const {client, fetch, recover} = setup()
 fetch.mockImplementation(async () => response({success: false, error: refusal}))
 await expect(client.call('settings.get')).rejects.toEqual(refusal)
 expect(fetch).toHaveBeenCalledTimes(2)
 expect(recover).toHaveBeenCalledOnce()
})

test('batches are never replayed, even with only read operations', async () => {
 const {client, fetch, recover} = setup()
 await expect(client.batch([{method: 'settings.get'}])).rejects.toEqual(refusal)
 expect(fetch).toHaveBeenCalledOnce()
 expect(recover).not.toHaveBeenCalled()
})

test('network and malformed responses are not session refusals', async () => {
 for(const outcome of [() => {throw Error('network')}, () => response({}), () => response(null)]) {
  const {client, fetch, recover} = setup()
  fetch.mockImplementation(async () => outcome())
  await expect(client.call('settings.get')).rejects.toBeDefined()
  expect(fetch).toHaveBeenCalledOnce()
  expect(recover).not.toHaveBeenCalled()
 }
})

test.each(['cancel', 'timeout'] as const)('%s during refresh settles without a replay', async kind => {
 vi.useFakeTimers()
 const {client, fetch, recover} = setup()
 recover.mockImplementation(() => new Promise<Response>(() => {}))
 const abort = new AbortController()
 const pending = expect(client.call('settings.get', {}, {signal: abort.signal})).rejects.toMatchObject({
  code: kind === 'cancel' ? 'request_canceled' : 'request_timeout',
 })
 await vi.advanceTimersByTimeAsync(0)
 if(kind === 'cancel') abort.abort()
 else await vi.advanceTimersByTimeAsync(302000)
 await pending
 expect(fetch).toHaveBeenCalledOnce()
 // The shared refresh has its own allowance; finish it before restoring timers.
 await vi.advanceTimersByTimeAsync(302000)
 expect(vi.getTimerCount()).toBe(0)
})

test('refresh and replay share the original request allowance', async () => {
 vi.useFakeTimers()
 const {client, fetch, recover} = setup()
 recover.mockImplementation(() => new Promise(resolve => setTimeout(() => resolve(response({success:true, SynoToken:'fresh'})), 301000)))
 fetch.mockImplementationOnce(async () => response({success:false, error:refusal}))
  .mockImplementation(() => new Promise(() => {}))
 const pending = expect(client.call('settings.get')).rejects.toMatchObject({code:'request_timeout'})
 await vi.advanceTimersByTimeAsync(302000)
 await pending
 expect(fetch).toHaveBeenCalledTimes(2)
 expect(vi.getTimerCount()).toBe(0)
})
