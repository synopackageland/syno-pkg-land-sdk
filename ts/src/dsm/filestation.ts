import {createDsmSession} from './session.js'
import {clientTimeout} from '../context'
import type {ListOptions} from '../nas/contract'

// Destination URLs and API names are never consumer-controlled. An App URL,
// OIDC token or a backend session is not a DSM WebAPI credential.
export const DSM_FILE_ENDPOINT='/webapi/entry.cgi'
export const MAX_UPLOAD_BYTES=16*1024**3
export interface DsmFileOptions {token?:()=>string|undefined;timeout?:number}
export interface DsmFileEntry {name:string;path:string;isdir:boolean;additional?:{size?:number}}
export interface DsmFilePage {files?:DsmFileEntry[];shares?:DsmFileEntry[];offset:number;total:number}
function checkedEntry(value:unknown):DsmFileEntry{
 const entry=value as DsmFileEntry|null
 if(entry?.additional!=null&&(typeof entry.additional!=='object'||Array.isArray(entry.additional)))throw {code:'unavailable'}
 if(!entry||typeof entry.name!=='string'||typeof entry.path!=='string'||typeof entry.isdir!=='boolean'||entry.additional?.size!==undefined&&(!Number.isSafeInteger(entry.additional.size)||entry.additional.size<0))throw {code:'unavailable'}
 return entry.additional===null?{...entry,additional:undefined}:entry
}
async function metadata(response:Response):Promise<any>{
 const reader=response.body?.getReader()
 if(!reader)throw Error()
 const chunks:Uint8Array[]=[];let size=0
 try{
  for(;;){
   const {done,value}=await reader.read()
   if(done)break
   size+=value.byteLength
   if(size>1024*1024){await reader.cancel();throw Error()}
   chunks.push(value)
  }
 }finally{reader.releaseLock()}
 const bytes=new Uint8Array(size);let offset=0
 for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length}
 return JSON.parse(new TextDecoder().decode(bytes))
}
export function fileError(code:unknown):never{
 const codes:Record<string,string>={'106':'session_required','107':'session_required','119':'session_required','105':'denied','403':'denied','407':'denied','408':'denied','414':'conflict','1805':'conflict','400':'invalid-request'}
 throw {code:codes[String(code)]??'unavailable'}
}
export function filePath(path:string):void{
 if(typeof path!=='string'||path.length<2||path.length>4096||!path.startsWith('/')||/[\u0000-\u001f\u007f\\,"]/.test(path)||path.split('/').slice(1).some(p=>!p||p==='.'||p==='..'))throw {code:'invalid-request'}
}
export function fileName(name:string):void{
 if(typeof name!=='string'||!name||name.length>255||name==='.'||name==='..'||/[\u0000-\u001f\u007f/\\,"]/.test(name))throw {code:'invalid-request'}
}
function desktopToken():string|undefined{return (globalThis as typeof globalThis&{SYNO?:{SDS?:{Session?:{SynoToken?:string}}}}).SYNO?.SDS?.Session?.SynoToken}

export function createDsmFileStation(options:DsmFileOptions={}){
 // Bind lazily so host teardown and fetch test doubles cannot retain an old
 // transport. The factory is also safe to construct outside a DSM desktop.
 const session=createDsmSession({read:options.token??desktopToken,fetch:(_url,init)=>globalThis.fetch('/webman/login.cgi?enable_syno_token=yes',init)})
 async function token(signal?:AbortSignal){
  if(signal?.aborted)throw {code:'canceled'}
  const value=await session.ready()
  if(signal?.aborted)throw {code:'canceled'}
  if(!value)throw {code:'dsm_session_required'}
  return value
 }
 async function send(api:string,method:string,version:number,params:Record<string,string>,signal:AbortSignal,mutation=false,upload?:File):Promise<unknown>{
  const credential=await token(signal)
  const controller=new AbortController()
  const body=upload?new FormData():new URLSearchParams()
  for(const [k,v] of Object.entries({api,method,version:String(version),...params,SynoToken:credential}))body.append(k,v)
  if(upload)(body as FormData).append('file',upload)
  let sent=false,timeout=false
  const abort=()=>controller.abort()
  signal.addEventListener('abort',abort,{once:true})
  if(signal.aborted)controller.abort()
  const timer=setTimeout(()=>{timeout=true;controller.abort()},options.timeout??(upload?20*60*1000:clientTimeout()))
  try{
   if(controller.signal.aborted)throw {code:'canceled'}
   sent=true
   const res=await fetch(DSM_FILE_ENDPOINT,{method:'POST',body,credentials:'same-origin',mode:'same-origin',redirect:'error',cache:'no-store',signal:controller.signal,headers:{'X-SYNO-TOKEN':credential}})
   if(!res.ok)throw Error()
   const reply=await metadata(res)
   if(reply?.success===false){fileError(reply.error?.code)}
   if(reply?.success!==true)throw Error()
   // DSM can return errors inside a success envelope; never acknowledge them.
   if(reply.data?.error||Array.isArray(reply.data?.errors)&&reply.data.errors.length)throw {code:'denied'}
   return reply.data
  }catch(e){
   const code=(e as {code?:unknown})?.code
   // DOMException also has a numeric code (AbortError is 20); only DSM's
   // known refusal numbers are DSM errors. An aborted dispatched upload
   // remains uncertain rather than becoming a generic unavailable reply.
   if(typeof code==='number'&&[106,107,119,105,403,407,408,414,1805,400].includes(code))fileError(code)
   if(typeof code==='string'&&['session_required','dsm_session_required','denied','not_found','conflict','invalid-request','unavailable','commit-uncertain','canceled','timeout'].includes(code))throw {code}
   throw {code:mutation&&sent?'commit-uncertain':timeout?'timeout':signal.aborted?'canceled':'unavailable'}
  }finally{clearTimeout(timer);signal.removeEventListener('abort',abort)}
 }
 async function page(path:string|undefined,offset:number,options:ListOptions,signal:AbortSignal){
  if(path!==undefined)filePath(path)
  const limit=options.limit??100,sort=options.sortBy??'name',direction=options.sortDirection??'ASC'
  if(!Number.isSafeInteger(offset)||offset<0||offset>1000000||!Number.isSafeInteger(limit)||limit<1||limit>100||!['name','size','type','mtime'].includes(sort)||!['ASC','DESC'].includes(direction))throw {code:'invalid-request'}
  const page=await send('SYNO.FileStation.List',path===undefined?'list_share':'list',2,{offset:String(offset),limit:String(limit),sort_by:sort,sort_direction:direction,additional:'["size"]',...(path===undefined?{}:{folder_path:path})},signal) as DsmFilePage
  const entries=path===undefined?page?.shares??page?.files:page?.files
  if(!Array.isArray(entries)||entries.length>limit||!Number.isSafeInteger(page.total)||page.total<0||page.offset!==offset||offset+entries.length>page.total||!entries.length&&offset<page.total)throw {code:'unavailable'}
  entries.forEach(checkedEntry)
  return page
 }
 return {
  token,
  shares:(offset:number,signal:AbortSignal)=>page(undefined,offset,{},signal),
  list:(path:string,options:ListOptions,signal:AbortSignal)=>page(path,options.offset??0,options,signal),
  async info(path:string,signal:AbortSignal){
   filePath(path)
   const data=await send('SYNO.FileStation.List','getinfo',2,{path:JSON.stringify([path]),additional:'["size"]'},signal) as {files?:DsmFileEntry[]}
   if(!Array.isArray(data?.files)||data.files.length!==1||data.files[0]?.path!==path)throw {code:'not_found'}
   return checkedEntry(data.files[0])
  },
  async createFolder(parent:string,name:string,signal:AbortSignal){filePath(parent);fileName(name);await send('SYNO.FileStation.CreateFolder','create',2,{folder_path:JSON.stringify([parent]),name:JSON.stringify([name]),force_parent:'false'},signal,true);return {created:true}},
  // Display/preview only. A successful browser result never authorizes a
  // backend write or grant; the executor independently checks its subject.
  async checkPermission(path:string,signal:AbortSignal){
   filePath(path)
   const nonce=Array.from(crypto.getRandomValues(new Uint8Array(16)),b=>b.toString(16).padStart(2,'0')).join('')
   const data=await send('SYNO.FileStation.CheckPermission','write',3,{path,filename:`.synopkgland-permission-probe-${nonce}`,overwrite:'false',size:'0'},signal) as {blSkip?:boolean}
   if(data?.blSkip!==false)throw {code:'denied'}
  },
  async upload(parent:string,file:File,signal:AbortSignal){
   filePath(parent);fileName(file.name)
   if(!Number.isSafeInteger(file.size)||file.size<0||file.size>MAX_UPLOAD_BYTES)throw {code:'upload_size_required'}
   await send('SYNO.FileStation.Upload','upload',2,{path:parent,create_parents:'false',overwrite:'false',size:String(file.size)},signal,true,file)
  },
 }
}
