import {afterEach, expect, test, vi} from 'vitest'
import {createDsmSession} from './session.js'

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
 await vi.advanceTimersByTimeAsync(15000)
 expect(await pending).toBeUndefined()
 expect(fetch.mock.calls[0]?.[1]?.signal?.aborted).toBe(true)
})

test('a response arriving after the deadline cannot publish a stale token',async()=>{
 vi.useFakeTimers()
 let resolve!:(response:Response)=>void
 const write=vi.fn(),session=createDsmSession({read:()=>undefined,write,fetch:()=>new Promise<Response>(r=>{resolve=r})})
 const pending=session.ready()
 await vi.advanceTimersByTimeAsync(15000)
 expect(await pending).toBeUndefined()
 resolve(response({success:true,SynoToken:'late'}))
 await Promise.resolve();await Promise.resolve()
 expect(write).not.toHaveBeenCalled()
 expect(session.token()).toBeUndefined()
})
