import {afterEach, expect, test, vi} from 'vitest'
import {createDsmSession} from './session.js'
import {clientTimeout} from '../context'

afterEach(()=>vi.useRealTimers())
const response=(body:unknown,ok=true)=>({ok,json:async()=>body}) as Response

test('desktop tokens stay reactive and never start a recovery request',async()=>{
 let token='desktop'
 const fetch=vi.fn()
 const session=createDsmSession({read:()=>token,fetch})
 expect(await session.ready()).toBe('desktop')
 token='renewed'
 expect(await session.ready()).toBe('renewed')
 expect(fetch).not.toHaveBeenCalled()
})

test('a portal recovers the current token once, before concurrent requests proceed',async()=>{
 let resolve!:(response:Response)=>void
 const fetch=vi.fn(()=>new Promise<Response>(r=>{resolve=r}))
 let token:string|undefined
 const write=vi.fn((value:string)=>{token=value})
 const session=createDsmSession({read:()=>token,write,fetch})
 const first=session.ready(),second=session.ready()
 expect(fetch).toHaveBeenCalledTimes(1)
 expect(fetch).toHaveBeenCalledWith('webman/login.cgi?enable_syno_token=yes',expect.objectContaining({method:'GET',credentials:'same-origin',mode:'same-origin',redirect:'error',cache:'no-store',signal:expect.any(AbortSignal)}))
 expect(write).not.toHaveBeenCalled()
 resolve(response({success:true,SynoToken:'current-session-token'}))
 expect(await first).toBe('current-session-token')
 expect(await second).toBe('current-session-token')
 expect(session.token()).toBe('current-session-token')
 expect(write).toHaveBeenCalledOnce()
})

test('a desktop token arriving during recovery wins over the older reply',async()=>{
 let resolve!:(response:Response)=>void,token:string|undefined
 const write=vi.fn()
 const session=createDsmSession({read:()=>token,write,fetch:()=>new Promise<Response>(r=>{resolve=r})})
 const pending=session.ready()
 token='new-desktop-token'
 resolve(response({success:true,SynoToken:'old-reply'}))
 expect(await pending).toBe('new-desktop-token')
 expect(write).not.toHaveBeenCalled()
})

test.each([
 {success:false,SynoToken:'refused'}, {success:true}, {success:true,SynoToken:123},
 {success:true,SynoToken:''}, {success:true,SynoToken:'x\r\nInjected: 1'},
 {success:true,SynoToken:'x'.repeat(4097)}, {success:true,SynoToken:'x\u0085'},
])('failed or malformed recovery never supplies credentials or loops: %j',async body=>{
 const fetch=vi.fn(async()=>response(body)),write=vi.fn()
 const session=createDsmSession({read:()=>undefined,write,fetch})
 expect(await session.ready()).toBeUndefined()
 expect(await session.ready()).toBeUndefined()
 expect(write).not.toHaveBeenCalled()
 expect(fetch).toHaveBeenCalledOnce()
})

test.each(['HTTP','JSON','network'])('%s failures leave the existing credential refusal path intact',async kind=>{
 const fetch=vi.fn(async()=>{
  if(kind==='network')throw Error('unavailable')
  if(kind==='JSON')return {ok:true,json:async()=>{throw Error('JSON')}} as unknown as Response
  return response({success:true,SynoToken:'ignored'},false)
 })
 const session=createDsmSession({read:()=>undefined,fetch})
 expect(await session.ready()).toBeUndefined()
})

test('a recovery deadline aborts the request and settles even if fetch ignores abort',async()=>{
 vi.useFakeTimers()
 const fetch=vi.fn((_input:RequestInfo|URL,_init?:RequestInit)=>new Promise<Response>(()=>{}))
 const session=createDsmSession({read:()=>undefined,fetch})
 const pending=session.ready()
 await vi.advanceTimersByTimeAsync(clientTimeout())
 expect(await pending).toBeUndefined()
 expect(fetch.mock.calls[0]?.[1]?.signal?.aborted).toBe(true)
})

test('a response arriving after the deadline cannot publish a stale token',async()=>{
 vi.useFakeTimers()
 let resolve!:(response:Response)=>void
 const write=vi.fn(),session=createDsmSession({read:()=>undefined,write,fetch:()=>new Promise<Response>(r=>{resolve=r})})
 const pending=session.ready()
 await vi.advanceTimersByTimeAsync(clientTimeout())
 expect(await pending).toBeUndefined()
 resolve(response({success:true,SynoToken:'late'}))
 await Promise.resolve();await Promise.resolve()
 expect(write).not.toHaveBeenCalled()
 expect(session.token()).toBeUndefined()
})

test('slow session recovery succeeds after sixty seconds',async()=>{
 vi.useFakeTimers()
 const session=createDsmSession({read:()=>undefined,fetch:()=>new Promise(resolve=>setTimeout(()=>resolve(response({success:true,SynoToken:'recovered'})),60000))})
 const pending=session.ready()
 await vi.advanceTimersByTimeAsync(60000)
 expect(await pending).toBe('recovered')
})

test('refresh replaces a rejected desktop token without needing a write callback',async()=>{
 let token='stale'
 const fetch=vi.fn(async()=>response({success:true,SynoToken:'fresh'}))
 const session=createDsmSession({read:()=>token,fetch})
 expect(await session.refresh('stale')).toBe('fresh')
 expect(await session.ready()).toBe('fresh')
 expect(session.token()).toBe('fresh')
 token='later-login'
 expect(session.token()).toBe('later-login')
 expect(fetch).toHaveBeenCalledOnce()
})

test('concurrent and late refusals share recovery and do not overwrite a newer desktop token',async()=>{
 let resolve!:(value:Response)=>void,token='stale'
 const fetch=vi.fn(()=>new Promise<Response>(r=>{resolve=r})),write=vi.fn()
 const session=createDsmSession({read:()=>token,write,fetch})
 const first=session.refresh('stale'),second=session.refresh('stale')
 expect(fetch).toHaveBeenCalledOnce()
 token='newer-desktop'
 resolve(response({success:true,SynoToken:'old-reply'}))
 expect(await first).toBe('newer-desktop')
 expect(await second).toBe('newer-desktop')
 expect(await session.refresh('stale')).toBe('newer-desktop')
 expect(write).not.toHaveBeenCalled()
 expect(fetch).toHaveBeenCalledOnce()
})

test('separate future logins can refresh again; a rejected token reply is never published',async()=>{
 const fetch=vi.fn().mockResolvedValueOnce(response({success:true,SynoToken:'fresh'}))
  .mockResolvedValueOnce(response({success:true,SynoToken:'newest'}))
  .mockResolvedValueOnce(response({success:true,SynoToken:'newest'}))
 const write=vi.fn(),session=createDsmSession({read:()=> 'stale',write,fetch})
 expect(await session.refresh('stale')).toBe('fresh')
 expect(await session.refresh('fresh')).toBe('newest')
 expect(await session.refresh('newest')).toBeUndefined()
 expect(write).toHaveBeenCalledTimes(2)
})

test.each(['refusal','same token','HTTP','JSON','network','throw','timeout'])(
 'refresh %s retains the desktop carrier during and after recovery',async kind=>{
  vi.useFakeTimers()
  let token='desktop'
  const write=vi.fn()
  const fetch=vi.fn(()=>{
   if(kind==='throw')throw Error('unavailable')
   if(kind==='timeout')return new Promise<Response>(()=>{})
   if(kind==='network')return Promise.reject(Error('unavailable'))
   if(kind==='JSON')return Promise.resolve({ok:true,json:async()=>{throw Error('bad JSON')}} as unknown as Response)
   return Promise.resolve(response({success:kind!=='refusal',SynoToken:'desktop'},kind!=='HTTP'))
  })
  const session=createDsmSession({read:()=>token,write,fetch})
  const pending=session.refresh('desktop')
  expect(session.token()).toBe('desktop')
  if(kind==='timeout')await vi.advanceTimersByTimeAsync(clientTimeout())
  expect(await pending).toBeUndefined()
  expect(session.token()).toBe('desktop')
  expect(await session.ready()).toBe('desktop')
  expect(write).not.toHaveBeenCalled()
  expect(fetch).toHaveBeenCalledOnce()
  token='next-desktop'
  expect(session.token()).toBe('next-desktop')
 })

test('a failed later refresh preserves the last recovered carrier',async()=>{
 const fetch=vi.fn().mockResolvedValueOnce(response({success:true,SynoToken:'fresh'}))
  .mockRejectedValueOnce(Error('unavailable'))
 const session=createDsmSession({read:()=> 'desktop',fetch})
 expect(await session.refresh('desktop')).toBe('fresh')
 expect(await session.refresh('fresh')).toBeUndefined()
 expect(session.token()).toBe('fresh')
})

test('a host write failure keeps the existing desktop carrier',async()=>{
 const session=createDsmSession({read:()=> 'desktop',write:()=>{throw Error('host unavailable')},
  fetch:async()=>response({success:true,SynoToken:'fresh'})})
 expect(await session.refresh('desktop')).toBeUndefined()
 expect(session.token()).toBe('desktop')
})
