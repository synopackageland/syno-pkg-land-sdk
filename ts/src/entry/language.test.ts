import {afterEach, expect, test, vi} from 'vitest'
import {readDsmLanguage} from './language'

const webApi = 'https://localhost/webapi/entry.cgi'
const settings = (Personal?: unknown) => ({api: 'SYNO.Core.UserSettings', success: true, data: Personal === undefined ? {Desktop: {}} : {Personal}})
const region = (language?: string) => language === undefined
 ? {api: 'SYNO.Core.Region.Language', success: false, error: {code: 105}}
 : {api: 'SYNO.Core.Region.Language', success: true, data: {language}}
const compound = (...result: unknown[]) => new Response(JSON.stringify({success: true, data: {result}}))

afterEach(() => {vi.useRealTimers()})

test.each([
 ['the personal language', [settings({lang: 'cht'}), region('def')], 'cht'],
 ['the system language when personal is def', [settings({lang: 'def'}), region('jpn')], 'jpn'],
 ['the browser when a user cannot read the system language', [settings(), region()], ''],
 ['the browser when both are def', [settings({lang: 'def'}), region('def')], ''],
 ['nothing when DSM refused the personal language', [{api: 'SYNO.Core.UserSettings', success: false, error: {code: 119}}, region('enu')], undefined],
 ['nothing for a malformed code', [settings({lang: '<b>'}), region()], ''],
])('reads %s', async (_name, result, want) => {
 const request = vi.fn<typeof globalThis.fetch>(async () => compound(...result))
 expect(await readDsmLanguage(request, webApi, 'token')).toBe(want)
 const [url, init] = request.mock.calls[0]!
 expect(url).toBe(webApi)
 expect(init).toMatchObject({method: 'POST', mode: 'same-origin', credentials: 'same-origin', redirect: 'error', headers: {'X-SYNO-TOKEN': 'token'}})
 const body = new URLSearchParams(String(init?.body))
 expect(body.get('api')).toBe('SYNO.Entry.Request')
 expect(JSON.parse(body.get('compound')!).map((call: {api: string}) => call.api)).toEqual(['SYNO.Core.UserSettings', 'SYNO.Core.Region.Language'])
})

test('a failed, refused or unreadable reply is no language', async () => {
 for(const response of [() => Promise.reject(new TypeError('offline')), async () => new Response('', {status: 502}),
  async () => new Response(JSON.stringify({success: false, error: {code: 119}})), async () => new Response('<html>')]) {
  expect(await readDsmLanguage(vi.fn<typeof globalThis.fetch>(response), webApi, 'token')).toBeUndefined()
 }
})

test('a DSM that never answers is abandoned after the client request deadline', async () => {
 vi.useFakeTimers({toFake: ['setTimeout', 'clearTimeout']})
 const request = vi.fn<typeof globalThis.fetch>((_url, init) => new Promise((_resolve, reject) => {
  init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
 }))
 const pending = readDsmLanguage(request, webApi, 'token', 1000)
 await vi.advanceTimersByTimeAsync(2999)
 expect(request.mock.calls[0]![1]?.signal?.aborted).toBe(false)
 await vi.advanceTimersByTimeAsync(1)
 expect(await pending).toBeUndefined()
})
