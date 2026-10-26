import {afterEach, expect, test, vi} from 'vitest'
import {readDsmPorts} from './service-ports'

afterEach(() => {vi.unstubAllGlobals(); vi.useRealTimers()})
const files = {token: () => 'fixture-token'}
const port_info = [
 {port_id: 'http', name: ' HTTP ', desc: 'Web Station', dst_port: ['80'], protocol: 'tcp'},
 {port_id: 'ntp', name: 'NTP', dst_port: ['123'], protocol: 'udp'},
 {port_id: 'mixed', dst_port: ['137:139','445','0','65536','9-1',null], protocol: 'tcp,udp'},
 {port_id: 'ftp', dst_port: ['21','55536-55899'], protocol: 'all'},
 null, {port_id: '', dst_port: ['1']}, {port_id: 'bad', dst_port: '80'},
]

test('the session channel returns localized names, single ports and unexpanded TCP/UDP ranges', async () => {
 const webApi = vi.fn(async () => ({port_info}))
 const fetch = vi.fn(); vi.stubGlobal('fetch', fetch)
 const result = await readDsmPorts({webApi})
 expect(webApi).toHaveBeenCalledWith('SYNO.Core.Service.PortInfo', 'load', 1, {})
 expect(fetch).not.toHaveBeenCalled()
 expect(result.map(p => p.id)).toEqual(['http','ntp','mixed','ftp'])
 expect(result[0]).toEqual({id:'http',name:'HTTP',desc:'Web Station',ports:[80],protocol:'tcp',ranges:[{first:80,last:80}]})
 expect(result[2]?.ranges).toEqual([{first:137,last:139},{first:445,last:445}])
 expect(result[3]?.ports).toEqual([21])
})

test('older windows use same-origin POST with the fresh session token', async () => {
 const fetch = vi.fn(async () => ({ok:true,json:async () => ({success:true,data:{port_info}})}))
 vi.stubGlobal('fetch', fetch)
 expect((await readDsmPorts({files})).length).toBe(4)
 expect(fetch).toHaveBeenCalledWith('/webapi/entry.cgi', expect.objectContaining({
  method:'POST',credentials:'same-origin',mode:'same-origin',redirect:'error',cache:'no-store',
  headers: {'Content-Type':'application/x-www-form-urlencoded','X-SYNO-TOKEN':'fixture-token'},
  signal:expect.any(AbortSignal),
 }))
 expect(new URLSearchParams(String((fetch.mock.calls as unknown[][])[0]?.[1] && ((fetch.mock.calls as unknown[][])[0]?.[1] as RequestInit).body)).get('version')).toBe('1')
})

test('missing session, DSM refusal, HTTP, JSON and transport failure are empty', async () => {
 const fetch = vi.fn(); vi.stubGlobal('fetch',fetch)
 expect(await readDsmPorts({})).toEqual([])
 expect(fetch).not.toHaveBeenCalled()
 for (const reply of [{success:false,error:{code:105}},{success:true,data:{}},{success:true,data:{port_info:null}}]) {
  fetch.mockResolvedValue({ok:true,json:async()=>reply})
  expect(await readDsmPorts({files})).toEqual([])
 }
 fetch.mockResolvedValue({ok:false}); expect(await readDsmPorts({files})).toEqual([])
 fetch.mockResolvedValue({ok:true,json:async()=>{throw Error('JSON')}}); expect(await readDsmPorts({files})).toEqual([])
 fetch.mockRejectedValue(Error('network')); expect(await readDsmPorts({files})).toEqual([])
 expect(await readDsmPorts({webApi:async()=>{throw Error('denied')}})).toEqual([])
})

test('a stuck session channel or fetch cannot keep a read pending past five seconds', async () => {
 vi.useFakeTimers()
 for (const ctx of [{webApi:() => new Promise<never>(()=>{})},{files}]) {
  const fetch = vi.fn((_url:string,_options:RequestInit)=>new Promise<never>(()=>{})); vi.stubGlobal('fetch',fetch)
  const pending = readDsmPorts(ctx)
  await vi.advanceTimersByTimeAsync(5000)
  expect(await pending).toEqual([])
  if (fetch.mock.calls.length) expect(fetch.mock.calls[0]?.[1].signal?.aborted).toBe(true)
 }
})
