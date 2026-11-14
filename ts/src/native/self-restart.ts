/** Protected native-host transport. Apps expose draft/preview/get to their
 * assistants; confirm, permits, progress and native receipts stay in this host.
 * No cookie, password, session or arbitrary command is persisted. */
export interface RestartOperation {
 reference:string
 package:string
 version:string
 state:'draft'|'accepted'|'stopping'|'starting'|'confirmed-running'|'refused'|'failed'|'unknown'
 reason?:string
}
export interface RestartPreview {
 reference:string
 package:string
 version:string
 digest:string
 action:'restart'|'acknowledge-unknown'
 interruptions:readonly string[]
 work:readonly {kind:string;reference:string}[]
 work_truncated:boolean
}
export interface SelfRestartOptions {
 // Installed build pins, never App/AI/request parameters.
 package:string
 version:string
 call(method:string,params:Record<string,unknown>):Promise<unknown>
 // Native DSM sendWebAPI returns unwrapped data. A native failure may reject
 // {code,nativeResult:response.errors} to preserve complete bulk failure
 // evidence; a transport failure has no nativeResult and remains unknown.
 send(api:string,method:string,version:number,params:Record<string,unknown>):Promise<unknown>
 // Reuse the App's protected administrator confirmation. It must return a
 // one-use server handle bound to ref/digest/actor, not a client boolean.
 confirm(preview:RestartPreview):Promise<string>
 isAdmin():boolean
}

export interface RestartNativeRequest {
 api:string
 method:string
 version:number
 params:Record<string,unknown>
 callback(success:boolean,response:unknown):void
}

/** Adapt the existing DSM window's sendWebAPI without dropping native failure
 * results. The host supplies scope/session handling; this helper saves neither. */
export function createSelfRestartTransport(send:(request:RestartNativeRequest)=>void):SelfRestartOptions['send']{
 return (api,method,version,params)=>new Promise((resolve,reject)=>{
  if(api!=='SYNO.Core.Package'||!['bulk','get'].includes(method)||version!==1){reject({code:'self_restart_refused'});return}
  send({api,method,version,params,callback(success,response){
   if(success){resolve(response);return}
   const error=response as {code?:unknown;errors?:unknown}|null
   reject({code:'dsm_'+String(error?.code??'error'),nativeResult:method==='bulk'?error?.errors:undefined})
  }})
 })
}
const ID=/^[a-f0-9]{64}$/
function fail(code:string):never{throw {code}}

export function createSelfRestart(options:SelfRestartOptions){
 let busy:Promise<RestartOperation>|undefined
 const spent=new Set<string>()
 function pinned<T extends {reference:string;package:string;version:string}>(value:T,expected?:string,historical=false):T{
  if(!value||!ID.test(value.reference)||(expected!==undefined&&value.reference!==expected)||value.package!==options.package||typeof value.version!=='string'||!value.version||value.version.length>128||(!historical&&value.version!==options.version))fail('self_restart_pin_mismatch')
  return value
 }
 async function get(reference:string):Promise<RestartOperation>{
  if(!ID.test(reference))fail('self_restart_unknown')
  return pinned(await options.call('package.restart.get',{reference}) as RestartOperation,reference,true)
 }
 async function draft():Promise<RestartOperation>{
  if(!options.isAdmin())fail('admin_required')
  return pinned(await options.call('package.restart.draft',{}) as RestartOperation)
 }
 async function preview(reference:string):Promise<RestartPreview>{
  if(!options.isAdmin())fail('admin_required')
  if(!ID.test(reference))fail('self_restart_unknown')
  const p=pinned(await options.call('package.restart.preview',{reference}) as RestartPreview,reference)
  if(p.action!=='restart'||!ID.test(p.digest)||!Array.isArray(p.work)||p.work.length>10||!Array.isArray(p.interruptions)||['all-apps','ai-turns','transfers','workers'].some(x=>!p.interruptions.includes(x)))fail('self_restart_bad_preview')
  return p
 }
 async function execute(reference:string):Promise<RestartOperation>{
  if(!options.isAdmin())fail('admin_required')
  // A local second call reads only. A new browser also receives dispatch:false
  // from the durable backend, including after loss of the admission reply.
  if(spent.has(reference))return get(reference)
  const p=await preview(reference)
  const approval=await options.confirm(p)
  if(typeof approval!=='string'||!approval||!options.isAdmin())fail('self_restart_refused')
  spent.add(reference)
  const d=await options.call('package.restart.confirm',{reference,approval}) as {dispatch:boolean;permit?:string;operation:RestartOperation;preview?:RestartPreview}
  pinned(d.operation,reference)
  if(d.dispatch!==true)return d.operation
  if(!d.permit||!ID.test(d.permit)||!d.preview||pinned(d.preview,reference).digest!==p.digest)fail('self_restart_bad_dispatch')
  const permit=d.permit
  const progress=(phase:string)=>options.call('package.restart.progress',{reference,permit,phase})
  // Do not dispatch unless this write was durably acknowledged. A lost reply
  // is not retried, even though this can leave an accepted operation unexecuted.
  await progress('stopping')
  let bulk:unknown,current:unknown
  function nativeUnknown(){
   return progress('unknown').then(value=>pinned(value as RestartOperation,reference),()=>({...d.operation,state:'unknown' as const,reason:'native-result-unavailable'}))
  }
  try{
   bulk=await options.send('SYNO.Core.Package','bulk',1,{
    operations:[{package:options.package,operation:'stop'},{package:options.package,operation:'start'}],reorder:false,
   })
  }catch(error){
   bulk=(error as {nativeResult?:unknown}|null)?.nativeResult
   // Sending may already have changed the package. Never replay bulk or start.
   if(!bulk)return nativeUnknown()
  }
  try{
   await progress('starting')
   current=await options.send('SYNO.Core.Package','get',1,{id:options.package,additional:['status']})
  }catch{current=null}
  // Record only the native observation. Backend independently validates the
  // complete tuple/sequence; running alone cannot confirm a restart. A lost
  // record reply is reconciled by get(reference), never another mutation.
  return pinned(await options.call('package.restart.record',{reference,permit,observation:{bulk,current}}) as RestartOperation,reference)
 }
 async function acknowledge(reference:string):Promise<RestartOperation>{
  if(!options.isAdmin())fail('admin_required')
  if(!ID.test(reference))fail('self_restart_unknown')
  const p=pinned(await options.call('package.restart.reconciliation_preview',{reference}) as RestartPreview,reference,true)
  if(p.action!=='acknowledge-unknown'||!ID.test(p.digest))fail('self_restart_bad_preview')
  const approval=await options.confirm(p)
  if(typeof approval!=='string'||!approval||!options.isAdmin())fail('self_restart_refused')
  return pinned(await options.call('package.restart.acknowledge',{reference,approval}) as RestartOperation,reference,true)
 }
 return {draft,preview,get,acknowledge,confirm(reference:string){
  if(busy)return Promise.reject({code:'self_restart_busy'})
  busy=execute(reference).finally(()=>{busy=undefined})
  return busy
 }}
}
