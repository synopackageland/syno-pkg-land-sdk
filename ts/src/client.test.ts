import {describe,it,expect,vi} from 'vitest'
import {createClient} from './client'

describe('batch client',()=>{
 it('sends one request and preserves independent errors',async()=>{
  const fetch=vi.fn(async(_url: RequestInfo | URL, _init?: RequestInit)=>({json:async()=>({success:true,data:{v:1,results:[{success:true,data:1},{success:false,error:{code:'admin_required',message:'admin'}},{success:true,data:3}]}})}))
  const client=createClient({endpoint:'/api.cgi',token:()=> 'token',fetch:fetch as unknown as typeof globalThis.fetch})
  const results=await client.batch([{method:'demo.read'},{method:'demo.admin'},{method:'demo.read'}])
  expect(fetch).toHaveBeenCalledTimes(1)
  expect(JSON.parse(fetch.mock.calls[0]![1]!.body as string)).toEqual({v:1,calls:[{method:'demo.read'},{method:'demo.admin'},{method:'demo.read'}]})
  expect(results.map(x=>x.success)).toEqual([true,false,true])
 })
 it('refuses limits and malformed replies without retrying',async()=>{
  const fetch=vi.fn(async(_url: RequestInfo | URL, _init?: RequestInit)=>({json:async()=>({success:true,data:{v:1,results:[]}})}))
  const client=createClient({endpoint:'/api.cgi',fetch:fetch as unknown as typeof globalThis.fetch})
  await expect(client.batch([])).rejects.toMatchObject({code:'bad_request'})
  expect(fetch).not.toHaveBeenCalled()
  await expect(client.batch([{method:'demo.read'}])).rejects.toMatchObject({code:'bad_response'})
  expect(fetch).toHaveBeenCalledTimes(1)
 })
})

describe('slow NAS deadlines',()=>{
 it.each(['call','batch'] as const)('keeps a slow %s alive, then times out once without replay',async kind=>{
  vi.useFakeTimers()
  try{
   const fetch=vi.fn((_url:RequestInfo|URL,init?:RequestInit)=>new Promise((_resolve,reject)=>{
    init!.signal!.addEventListener('abort',()=>reject(new Error('aborted')),{once:true})
   }))
   const client=createClient({endpoint:'/api.cgi',fetch:fetch as typeof globalThis.fetch})
   const seen=vi.fn()
   const pending=(kind==='call'?client.call('nas.join'):client.batch([{method:'nas.join'}])).catch(seen)
   await vi.advanceTimersByTimeAsync(301999)
   expect(seen).not.toHaveBeenCalled()
   expect(fetch.mock.calls[0]![1]!.signal!.aborted).toBe(false)
   await vi.advanceTimersByTimeAsync(1)
   await pending
   expect(seen).toHaveBeenCalledWith(expect.objectContaining({code:'request_timeout'}))
   expect(fetch).toHaveBeenCalledTimes(1)
   expect(vi.getTimerCount()).toBe(0)
  }finally{vi.useRealTimers()}
 })
 it('still honors an explicit shorter deadline and user cancellation',async()=>{
  vi.useFakeTimers()
  try{
   const fetch=vi.fn((_url:RequestInfo|URL,init?:RequestInit)=>new Promise((_resolve,reject)=>{
    init!.signal!.addEventListener('abort',()=>reject(new Error('aborted')),{once:true})
   }))
   const client=createClient({endpoint:'/api.cgi',requestTimeout:1000,fetch:fetch as typeof globalThis.fetch})
   const timed=expect(client.call('nas.join')).rejects.toMatchObject({code:'request_timeout'})
   await vi.advanceTimersByTimeAsync(3000)
   await timed
   const controller=new AbortController()
   const canceled=expect(client.call('nas.join',{}, {signal:controller.signal})).rejects.toMatchObject({code:'request_canceled'})
   controller.abort()
   await canceled
   expect(fetch).toHaveBeenCalledTimes(2)
   expect(vi.getTimerCount()).toBe(0)
  }finally{vi.useRealTimers()}
 })
})
