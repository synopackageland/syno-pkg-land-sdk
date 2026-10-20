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
