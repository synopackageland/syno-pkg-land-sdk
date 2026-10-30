import {readFileSync,readdirSync} from 'node:fs'
import {resolve} from 'node:path'
import {expect,expectTypeOf,test} from 'vitest'
import {generate} from '../scripts/gen-protocol.mjs'
import {protocolCodes,protocolOps,protocolSchemas,decodeResponse,encodeRequest,type ProtocolOp} from '../src/index'
import type {WebapiCallParams} from '../src/protocol/v1'

const spec=resolve(__dirname,'../../spec/protocol/v1')
const vectors=readdirSync(resolve(spec,'vectors')).flatMap(file=>readFileSync(resolve(spec,'vectors',file),'utf8').split('\n').filter(Boolean).map(line=>({file,...JSON.parse(line)})))

test('the committed protocol module is what the generator makes from spec/',()=>{
 expect(readFileSync(resolve(__dirname,'../src/protocol/v1.ts'),'utf8')).toBe(generate(spec))
})

test('codes and ops follow errors.json and schema/',()=>{
 const errors=JSON.parse(readFileSync(resolve(spec,'errors.json'),'utf8')).codes
 expect(Object.keys(protocolCodes)).toEqual(errors.map((c:{code:string})=>c.code))
 for(const c of errors)expect(protocolCodes[c.code as keyof typeof protocolCodes]).toEqual({kind:c.kind,retryable:c.retryable,httpStatus:c.httpStatus})
 expect([...protocolOps].sort()).toEqual(readdirSync(resolve(spec,'schema')).map(f=>f.replace(/\.json$/,'')).sort())
})

test('webapi.call types and wire metadata expose JSON and raw responses without request uploads',()=>{
 expectTypeOf<keyof WebapiCallParams>().toEqualTypeOf<'response_type'|'expected_size'|'delegation_id'|'job_id'|'api'|'method'|'version'|'params'>()
 expectTypeOf<WebapiCallParams['response_type']>().toEqualTypeOf<'json'|'raw'|undefined>()
 const schema=JSON.parse(readFileSync(resolve(spec,'schema/webapi.call.json'),'utf8'))
 expect(protocolSchemas['webapi.call'].params).toEqual(Object.keys(schema.properties))
 expect(protocolSchemas['webapi.call'].params).not.toContain('upload')
 expect(JSON.parse(encodeRequest('webapi.call',{delegation_id:'grant',api:'SYNO.FileStation.List',method:'list',version:2,params:{},response_type:'raw'})).params.response_type).toBe('raw')
})

test('every well-formed vector request encodes to one canonical line',()=>{
 const wellFormed=vectors.filter(v=>typeof v.request==='object'&&v.request.v===1&&protocolOps.includes(v.request.op)&&Object.keys(v.request).every(k=>['v','op','auth','params'].includes(k)))
 expect(wellFormed.length).toBeGreaterThan(20)
 for(const v of wellFormed){
  const line=encodeRequest(v.request.op as ProtocolOp,v.request.params,v.request.auth)
  expect(line.endsWith('\n'),v.name).toBe(true)
  expect(line.slice(0,-1)).not.toContain('\n')
  expect(JSON.parse(line),v.name).toEqual(v.request)
 }
})

test('every accepted vector uses only its schema params',()=>{
 for(const v of vectors.filter(v=>v.expect.ok===true)){
  const schema=protocolSchemas[v.request.op as ProtocolOp]
  for(const key of Object.keys(v.request.params))expect(schema.params,`${v.name}: ${key}`).toContain(key)
  for(const key of schema.required)expect(v.request.params,`${v.name}: ${key}`).toHaveProperty(key)
 }
})

test('a request line over 64 KiB is refused before it is sent',()=>{
 expect(()=>encodeRequest('files.write',{delegation_id:'a'.repeat(64),job_id:'j',staging:'s',dest_dir:'/home',name:'x'.repeat(70000)})).toThrow(expect.objectContaining({code:'REQUEST_TOO_LARGE'}))
})

test('every vector response decodes, and refusals keep their code',()=>{
 for(const v of vectors){
  // "<any>" matches any present value; an object stands in for it.
  const result=v.expect.result==='<any>'?{}:v.expect.result
  const decoded=decodeResponse(JSON.stringify(v.expect.ok?{ok:true,result}:{...v.expect,message:'m'}))
  if(v.expect.ok)expect(decoded,v.name).toEqual({ok:true,result})
  else expect(decoded,v.name).toEqual({ok:false,code:v.expect.code,message:'m'})
 }
})

test('a reply without a boolean ok, or with an unknown refusal code, is EXECUTOR_PROTOCOL',()=>{
 for(const line of ['not json','[]','{"ok":"yes"}','{"ok":true}','{"ok":true,"result":[]}','{"ok":false,"code":"CANCEL_REQUESTED","message":"m"}','{"ok":false,"code":"NOPE","message":"m"}'])
  expect(()=>decodeResponse(line),line).toThrow(expect.objectContaining({code:'EXECUTOR_PROTOCOL'}))
})
