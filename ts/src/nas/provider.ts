import {appDenial} from '../authorization/refusal'
import {createFileTransfers} from '../files/transfers'
import {clientTimeout,type Context} from '../context'
import type {NasEntry,NasPage,NasProvider} from './contract'

function refuse(code='unavailable'):never{throw {code}}
function record(v:unknown):Record<string,unknown>{if(!v||typeof v!=='object'||Array.isArray(v))refuse();return v as Record<string,unknown>}
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0
function entry(value:unknown):NasEntry{
 const v=record(value),additional=record(v.additional??{})
 if(typeof v.name!=='string'||typeof v.path!=='string'||typeof v.isdir!=='boolean'||(additional.size!==undefined&&!integer(additional.size)))refuse()
 // Display DSM's response without guessing permissions from path prefixes.
 return {name:v.name,path:v.path,isDir:v.isdir,...(additional.size===undefined?{}:{bytes:additional.size as number})}
}
// Folders DSM keeps for itself: the recycle bin, snapshots and its metadata.
// Nothing is saved or chosen inside them.
const systemFolders=new Set(['#recycle','#snapshot','@eaDir','@tmp','@sharebin'])
function page(value:unknown,offset:number,limit:number):NasPage{
 const v=record(value),raw=v.shares??v.files
 if(!Array.isArray(raw)||raw.length>limit||!integer(v.total)||v.offset!==offset||offset+raw.length>v.total)refuse()
 const entries=raw.map(entry),next=offset+entries.length
 if(next<v.total&&!entries.length)refuse()
 // Paging and total stay DSM's; only the shown entries skip system folders.
 return {entries:entries.filter(e=>!systemFolders.has(e.name)),total:v.total,offset,...(next<v.total?{nextOffset:next}:{})}
}
export function createNasProvider(ctx:Pick<Context,'call'|'files'|'requestTimeout'>):NasProvider{
 let disposed=false
 const life=new AbortController()
 const transfers=ctx.files?createFileTransfers(ctx.files):undefined
 async function request(method:string,params:Record<string,unknown>,signal:AbortSignal,mutation=false):Promise<unknown>{
  if(disposed||signal.aborted)refuse('canceled')
  let cleanup=()=>{}
  const stop=new Promise<never>((_,reject)=>{
   const abort=()=>reject({code:mutation?'commit-uncertain':'canceled'})
   const timer=setTimeout(()=>reject({code:mutation?'commit-uncertain':'timeout'}),clientTimeout(ctx.requestTimeout))
   signal.addEventListener('abort',abort,{once:true});life.signal.addEventListener('abort',abort,{once:true})
   cleanup=()=>{clearTimeout(timer);signal.removeEventListener('abort',abort);life.signal.removeEventListener('abort',abort)}
  })
  try{return await Promise.race([ctx.call(method,params),stop])}
  catch(e){
   const code=(e as {code?:unknown})?.code
   if(appDenial(e))refuse(appDenial(e))
   if([407,408,'407','408','denied'].includes(code as string|number))refuse('denied')
   // The backend refuses these before any handler runs, so nothing was committed.
   if(code==='cross_origin'||code==='origin_required')refuse('origin-refused')
   if(code==='request_timeout'||code==='request_canceled')refuse(mutation?'commit-uncertain':code==='request_timeout'?'timeout':'canceled')
   const allowed=['session_required','dsm_session_required','auth_unavailable','denied','not_found','conflict','invalid-request','unavailable','unknown_method','unsupported','commit-uncertain','canceled','timeout']
   refuse(typeof code==='string'&&allowed.includes(code)?code:mutation?'commit-uncertain':'unavailable')
  }finally{cleanup()}
 }
 return {
  ...(transfers?{upload:transfers.upload,download:transfers.download}:{}),
  async shares(offset,signal){return page(await request('nas.files.shares',{offset},signal),offset,100)},
  async list(path,options,signal){const offset=options.offset??0,limit=options.limit??100;return page(await request('nas.files.list',{path,...options,offset,limit},signal),offset,limit)},
  async info(path,signal){return entry(await request('nas.files.info',{path},signal))},
  async createFolder(parent,name,signal){const v=record(await request('nas.files.create_folder',{parent,name},signal,true));if(v.created!==true)refuse('commit-uncertain')},
  dispose(){disposed=true;life.abort();transfers?.dispose()},
 }
}
