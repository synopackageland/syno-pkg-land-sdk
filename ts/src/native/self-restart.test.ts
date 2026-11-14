import {test,expect,vi} from 'vitest'
import {createSelfRestart,createSelfRestartTransport,type SelfRestartOptions} from './self-restart'

const ref='a'.repeat(64),permit='b'.repeat(64),digest='c'.repeat(64)
const operation={reference:ref,package:'Example',version:'1.0.0-0001',state:'accepted'}
const preview={...operation,action:'restart',digest,interruptions:['all-apps','ai-turns','transfers','workers'],work:[],work_truncated:false}
function fixture(){
 const call=vi.fn(async(method:string,_params:Record<string,unknown>):Promise<unknown>=>{
  if(method.endsWith('.preview'))return preview
  if(method.endsWith('.confirm'))return {operation,dispatch:true,permit,preview}
  if(method.endsWith('.get'))return {...operation,state:'unknown'}
  if(method.endsWith('.record'))return {...operation,state:'confirmed-running'}
  return operation
 })
 const send=vi.fn(async(_api:string,_method:string,_version:number,_params:Record<string,unknown>):Promise<unknown>=>({}))
 const confirm=vi.fn(async()=> 'one-use-approval')
 const options:SelfRestartOptions={package:'Example',version:'1.0.0-0001',call,send,confirm,isAdmin:()=>true}
 return {options,call,send,confirm}
}

test('fixed native batch is sent once and only after durable protected admission',async()=>{
 const {options,call,send,confirm}=fixture();const access=createSelfRestart(options)
 const out=await access.confirm(ref)
 expect(out.state).toBe('confirmed-running')
 expect(confirm).toHaveBeenCalledWith(preview)
 expect(call.mock.calls.map(c=>c[0])).toEqual(['package.restart.preview','package.restart.confirm','package.restart.progress','package.restart.progress','package.restart.record'])
 expect(send.mock.calls[0]).toEqual(['SYNO.Core.Package','bulk',1,{operations:[{package:'Example',operation:'stop'},{package:'Example',operation:'start'}],reorder:false}])
 expect(send.mock.calls[1]).toEqual(['SYNO.Core.Package','get',1,{id:'Example',additional:['status']}])
 expect(JSON.stringify(out)).not.toContain(permit)
 await access.confirm(ref)
 expect(send).toHaveBeenCalledTimes(2)
 expect(confirm).toHaveBeenCalledTimes(1)
})

test('lost admission or progress reply never sends the native mutation',async()=>{
 for(const failed of ['package.restart.confirm','package.restart.progress']){
  const {options,call,send}=fixture();const original=call.getMockImplementation()!
  call.mockImplementation(async(method,params)=>{if(method===failed)throw new Error('lost reply');return original(method,params)})
  const access=createSelfRestart(options)
  await expect(access.confirm(ref)).rejects.toBeDefined()
  expect(send).not.toHaveBeenCalled()
  await access.confirm(ref)
  expect(send).not.toHaveBeenCalled()
 }
})

test('lost native reply records unknown and never retries bulk or a separate start',async()=>{
 const {options,send,call}=fixture();send.mockRejectedValue({code:'transport-lost'})
 const original=call.getMockImplementation()!
 call.mockImplementation(async(method,params)=>method.endsWith('.progress')&&params.phase==='unknown'?{...operation,state:'unknown'}:original(method,params))
 const access=createSelfRestart(options)
 expect((await access.confirm(ref)).state).toBe('unknown')
 await access.confirm(ref)
 expect(send).toHaveBeenCalledTimes(1)
 expect(call.mock.calls.some(([method,params])=>method.endsWith('.record')||params.phase==='starting')).toBe(false)
})

test('durable duplicate, non-admin, changed pin and changed preview refuse dispatch',async()=>{
 for(const variant of ['duplicate','non-admin','pin','digest']){
  const {options,call,send}=fixture();const original=call.getMockImplementation()!
  if(variant==='non-admin')options.isAdmin=()=>false
  call.mockImplementation(async(method,params)=>{
   const out=await original(method,params)
   if(method.endsWith('.confirm')){
    if(variant==='duplicate')return {operation,dispatch:false}
    if(variant==='digest')return {operation,dispatch:true,permit,preview:{...preview,digest:'d'.repeat(64)}}
   }
   if(method.endsWith('.preview')&&variant==='pin')return {...preview,package:'Other'}
   return out
  })
  const task=createSelfRestart(options).confirm(ref)
  if(variant==='duplicate')expect((await task).state).toBe('accepted')
  else await expect(task).rejects.toBeDefined()
  expect(send).not.toHaveBeenCalled()
 }
})

test('complete native failure evidence is recorded even if the package stays stopped',async()=>{
 const {options,call,send}=fixture();const failure={success:false,results:[{action:'stop',success:true},{action:'start',success:false}]}
 send.mockRejectedValueOnce({code:'dsm_4501',nativeResult:failure}).mockResolvedValueOnce({id:'Example',additional:{status:'stop'}})
 const original=call.getMockImplementation()!
 call.mockImplementation(async(method,params)=>method.endsWith('.record')?{...operation,state:'failed',reason:'start-failed'}:original(method,params))
 const out=await createSelfRestart(options).confirm(ref)
 expect(out.state).toBe('failed')
 expect(call.mock.calls.at(-1)).toEqual(['package.restart.record',{reference:ref,permit,observation:{bulk:failure,current:{id:'Example',additional:{status:'stop'}}}}])
 expect(send).toHaveBeenCalledTimes(2)
})

test('acknowledging manual reconciliation never sends a lifecycle request',async()=>{
 const {options,call,send,confirm}=fixture();const p={...preview,action:'acknowledge-unknown',interruptions:[]}
 call.mockImplementation(async(method)=>method.endsWith('.reconciliation_preview')?p:{...operation,state:'unknown',reason:'operator-acknowledged'})
 const out=await createSelfRestart(options).acknowledge(ref)
 expect(out.state).toBe('unknown');expect(confirm).toHaveBeenCalledWith(p)
 expect(send).not.toHaveBeenCalled()
 expect(call.mock.calls.map(c=>c[0])).toEqual(['package.restart.reconciliation_preview','package.restart.acknowledge'])
})

test('native transport preserves batch failure details and refuses unrelated methods',async()=>{
 const evidence={success:false,results:[]}
 const send=vi.fn((request)=>request.callback(false,{code:4501,errors:evidence}))
 const transport=createSelfRestartTransport(send)
 await expect(transport('SYNO.Core.Package','bulk',1,{})).rejects.toEqual({code:'dsm_4501',nativeResult:evidence})
 await expect(transport('SYNO.Core.Package.Control','restart',1,{})).rejects.toEqual({code:'self_restart_refused'})
 expect(send).toHaveBeenCalledTimes(1)
})

test('an upgraded App can read and acknowledge old references without executing them',async()=>{
 const {options,call,send}=fixture();options.version='2.0.0-0001'
 call.mockImplementation(async(method)=>method.endsWith('.reconciliation_preview')?{...preview,action:'acknowledge-unknown',interruptions:[]}:{...operation,state:'unknown'})
 const access=createSelfRestart(options)
 expect((await access.get(ref)).version).toBe('1.0.0-0001')
 expect((await access.acknowledge(ref)).state).toBe('unknown')
 expect(send).not.toHaveBeenCalled()
})
