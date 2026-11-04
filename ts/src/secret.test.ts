import {expect,test,vi} from 'vitest'
import {requestSecret} from './secret'
import {createClient} from './client'

test('request-only secrets are redacted in ordinary JSON and string output',()=>{
 const value=requestSecret('canary')
 expect(String(value)).toBe('[redacted]')
 expect(JSON.stringify({value})).toBe('{"value":"[redacted]"}')
 expect(JSON.stringify({...value})).not.toContain('canary')
})

test.each(['success','failure','timeout'])('one-shot transport consumes secrets on %s',async mode=>{
 const value=requestSecret('request-canary')
 const fetch=vi.fn(async (_url:unknown,init?:RequestInit)=>{
  expect(JSON.parse(String(init?.body)).params.credential).toBe('request-canary')
  if(mode==='failure')throw new Error('network')
  if(mode==='timeout')return new Promise<Response>((_,reject)=>init?.signal?.addEventListener('abort',()=>reject(new Error('abort'))))
  return new Response('{"success":true,"data":null}')
 })
 const client=createClient({endpoint:'/api',fetch:fetch as typeof globalThis.fetch,requestTimeout:1})
 await client.call('demo.connect',{credential:value}).catch(()=>{})
 expect(JSON.stringify(value)).toBe('"[redacted]"')
 await expect(client.call('demo.connect',{credential:value})).rejects.toMatchObject({code:'secret_consumed'})
 expect(fetch).toHaveBeenCalledTimes(1)
})

test('transport errors and response exceptions never expose raw credential values',async()=>{
 const value=requestSecret('unlabelled-canary')
 const client=createClient({endpoint:'/api',fetch:async()=>{throw new Error('unlabelled-canary')}})
 const error=await client.call('demo.connect',{credential:value}).catch(error=>error)
 expect(String(error)+JSON.stringify(error)).not.toContain('unlabelled-canary')
})

test('ordinary params retain native JSON serialization',async()=>{
 const fetch=vi.fn(async (_url:unknown,init?:RequestInit)=>{
  expect(JSON.parse(String(init?.body)).params.when).toBe('2026-01-01T00:00:00.000Z')
  return new Response('{"success":true,"data":null}')
 })
 await createClient({endpoint:'/api',fetch:fetch as typeof globalThis.fetch}).call('demo.connect',{when:new Date('2026-01-01')})
})

test('public error fields and credential echoes are redacted recursively',async()=>{
 const client=createClient({endpoint:'/api',fetch:async()=>new Response(JSON.stringify({success:false,error:{code:'refused',message:'external refused unlabelled-canary',details:{token_value:'canary-t',otp:'123456'}}}))})
 const error=await client.call('demo.connect',{credential:requestSecret('unlabelled-canary')}).catch(error=>error)
 expect(JSON.stringify(error)).not.toContain('canary')
 expect(JSON.stringify(error)).not.toContain('123456')
 expect(error.code).toBe('refused')
})
