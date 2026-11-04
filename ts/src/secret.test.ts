import {expect,test,vi} from 'vitest'
import {requestSecret,redactSecrets} from './secret'
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

test('transport errors retain their original identity',async()=>{
 const value=requestSecret('unlabelled-canary')
 const original=new Error('unlabelled-canary')
 const client=createClient({endpoint:'/api',fetch:async()=>{throw original}})
 const error=await client.call('demo.connect',{credential:value}).catch(error=>error)
 expect(error).toBe(original)
})

test('ordinary params retain native JSON serialization',async()=>{
 const fetch=vi.fn(async (_url:unknown,init?:RequestInit)=>{
  expect(JSON.parse(String(init?.body)).params.when).toBe('2026-01-01T00:00:00.000Z')
  return new Response('{"success":true,"data":null}')
 })
 await createClient({endpoint:'/api',fetch:fetch as typeof globalThis.fetch}).call('demo.connect',{when:new Date('2026-01-01')})
})

test.each([false,true])('public errors retain all fields with request secret=%s',async withSecret=>{
 const error={code:'token_count',message:'external refused unlabelled-canary',details:{token:'t',password:'p',sid:'s',tokenCount:3,sidebar:'left',secretName:'label',otpEnabled:true,token_value:'canary-t',otp:'123456'}}
 const client=createClient({endpoint:'/api',fetch:async()=>new Response(JSON.stringify({success:false,error}))})
 await expect(client.call('demo.connect',withSecret?{credential:requestSecret('unlabelled-canary')}:{})).rejects.toEqual(error)
})

test('diagnostics redact only exact names and declared application fields',()=>{
 const input={password:'p',Password:'P',passwd:'p',passphrase:'p',otp:'o',token:'t',secret:'s',authorization:'a',cookie:'c',synotoken:'s',sid:'s','X-SYNO-TOKEN':'s',appCredential:'a',tokenCount:3,sidebar:'left',secretName:'label',otpEnabled:true,api_token:'visible',passwordHint:'visible'}
 const expected={...input}
 for(const key of ['password','Password','passwd','passphrase','otp','token','secret','authorization','cookie','synotoken','sid','X-SYNO-TOKEN','appCredential']) (expected as Record<string,unknown>)[key]='[redacted]'
 expect(redactSecrets(input,[],['appCredential'])).toEqual(expected)
 expect(input.password).toBe('p')
})

test('request-only credentials disable opted-in read recovery and replay',async()=>{
 const refresh=vi.fn(async()=> 'fresh')
 const fetch=vi.fn(async()=>new Response('{"success":false,"error":{"code":"session_required","message":"sign in"}}'))
 const client=createClient({endpoint:'/api',fetch,sessionRetry:{session:{token:()=> 'stale',ready:async()=> 'stale',refresh},operations:{'demo.connect':{effect:'read',kind:'rpc'}}}})
 await expect(client.call('demo.connect',{credential:requestSecret('canary')})).rejects.toMatchObject({code:'session_required'})
 expect(fetch).toHaveBeenCalledTimes(1)
 expect(refresh).not.toHaveBeenCalled()
})
