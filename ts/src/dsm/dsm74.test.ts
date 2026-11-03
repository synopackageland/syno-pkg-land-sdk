// @vitest-environment jsdom
// DSM 7.4.1 as it behaves on a real NAS:
// - an AppWindow's sendWebAPI JSON-encodes every parameter value
//   (SYNO.API.EncodeParams: "x" -> "\"x\"", ["/a"] -> "[\"/a\"]"), and WebAPI
//   decodes each value as JSON, so a value that is already JSON text arrives as
//   a string and File Station refuses it (401);
// - WebAPI refuses a SynoToken in a POST body (119) and accepts it in the
//   X-SYNO-TOKEN header or the query string. DSM 7.1.1 answers the same (a
//   cookie session's File Station List: header and query accepted, body or no
//   token 119), so the body placement was never valid and nothing here
//   depends on the DSM version.
import {afterEach,expect,test,vi} from 'vitest'
import {createNasProvider} from '../nas/provider'
import {createFileTransfers} from '../files/transfers'
import {createDsmFileStation} from './filestation'
import {readDsmPorts} from './service-ports'

afterEach(()=>vi.unstubAllGlobals())
const signal=()=>new AbortController().signal
const TOKEN='dsm74-token'

/** WebAPI's own decoding of one parameter: JSON when it parses, else the text. */
function decode(value:string):unknown{try{return JSON.parse(value)}catch{return value}}
/** A path parameter File Station takes: one path or a list of paths. */
function paths(value:unknown):string[]|undefined{
 if(typeof value==='string'&&value.startsWith('/'))return [value]
 return Array.isArray(value)&&value.every(v=>typeof v==='string'&&v.startsWith('/'))?value:undefined
}
/** File Station on DSM 7.4.1 for decoded parameters; records what it was asked. */
function fileStation(){
 const seen:Record<string,unknown>[]=[]
 const reply=(p:Record<string,unknown>):{success:boolean;data?:unknown;error?:{code:number}}=>{
  seen.push(p)
  const fail=(code:number)=>({success:false,error:{code}})
  switch(`${p.api} ${p.method}`){
   case 'SYNO.FileStation.List list_share':return {success:true,data:{shares:[{name:'team',path:'/team',isdir:true}],offset:Number(p.offset),total:1}}
   case 'SYNO.FileStation.List list':{
    const [folder]=paths(p.folder_path)??[]
    if(!folder)return fail(401)
    if(!Array.isArray(p.additional))return fail(401)
    return {success:true,data:{files:[],offset:Number(p.offset),total:0}}
   }
   case 'SYNO.FileStation.List getinfo':{
    const list=paths(p.path);if(!list)return fail(401)
    return {success:true,data:{files:list.map(path=>({name:path.split('/').pop(),path,isdir:false,additional:{size:1}}))}}
   }
   case 'SYNO.FileStation.CreateFolder create':{
    const parent=paths(p.folder_path),name=p.name
    if(!parent||!(typeof name==='string'||Array.isArray(name)&&name.every(n=>typeof n==='string')))return fail(401)
    return {success:true,data:{folders:[]}}
   }
   case 'SYNO.FileStation.CheckPermission write':return paths(p.path)?{success:true,data:{blSkip:false}}:fail(401)
   case 'SYNO.FileStation.Upload upload':return paths(p.path)?{success:true,data:{}}:fail(401)
   case 'SYNO.Core.Service.PortInfo load':return {success:true,data:{port_info:[{port_id:'http',dst_port:['80'],protocol:'tcp'}]}}
  }
  return fail(103)
 }
 return {seen,reply}
}
/** A DSM AppWindow's sendWebAPI: every value JSON-encoded, token in the header. */
function dsmWindow(station=fileStation()){
 const win={sendWebAPI(o:{api:string;method:string;version:number;params:Record<string,unknown>;callback:(ok:boolean,data:unknown)=>void}){
  const wire=Object.fromEntries(Object.entries(o.params).map(([k,v])=>[k,JSON.stringify(v)]))
  const r=station.reply({api:o.api,method:o.method,...Object.fromEntries(Object.entries(wire).map(([k,v])=>[k,decode(v)]))})
  queueMicrotask(()=>r.success?o.callback(true,r.data):o.callback(false,r.error))
 }}
 // The CLI's window bridge (renderWebApi): resolves data, rejects {code:"dsm_<n>"}.
 const webApi=(api:string,method:string,version:number,params:Record<string,unknown>)=>new Promise((resolve,reject)=>
  win.sendWebAPI({api,method,version,params,callback:(ok,data)=>ok?resolve(data):reject({code:`dsm_${(data as {code?:number})?.code??'error'}`})}))
 return {webApi,station}
}
/** DSM's /webapi/entry.cgi over fetch: a body SynoToken is refused with 119. */
function dsmFetch(station=fileStation()){
 const requests:{url:string;header?:string;bodyToken:boolean;query:URLSearchParams}[]=[]
 const fetch=vi.fn(async(url:string,init?:RequestInit)=>{
  const u=new URL(url,'https://nas.invalid'),body=init?.body
  const form=body instanceof URLSearchParams||body instanceof FormData?body:new URLSearchParams()
  const header=new Headers(init?.headers).get('X-SYNO-TOKEN')??undefined
  requests.push({url,header,bodyToken:form.has('SynoToken'),query:u.searchParams})
  if(u.pathname==='/webman/login.cgi')return new Response(JSON.stringify({success:true,SynoToken:TOKEN}))
  if(form.has('SynoToken'))return new Response(JSON.stringify({success:false,error:{code:119}}))
  if(header!==TOKEN&&u.searchParams.get('SynoToken')!==TOKEN)return new Response(JSON.stringify({success:false,error:{code:119}}))
  const params:Record<string,unknown>={}
  const entries:[string,FormDataEntryValue][]=[...u.searchParams];form.forEach((v,k)=>{entries.push([k,v])})
  for(const [k,v] of entries)if(typeof v==='string'&&k!=='SynoToken')params[k]=decode(v)
  return new Response(JSON.stringify(station.reply(params)))
 })
 vi.stubGlobal('fetch',fetch)
 return {fetch,requests,station}
}
const admitted={call:vi.fn(async()=>({mode:'browser-session'})) as any}

test('on DSM 7.4 the window channel reaches File Station with the paths and names the App gave', async()=>{
 const {webApi,station}=dsmWindow(),fetch=vi.fn();vi.stubGlobal('fetch',fetch)
 const p=createNasProvider({...admitted,webApi})
 expect((await p.shares(0,signal())).entries[0]?.path).toBe('/team')
 expect((await p.list('/team',{},signal())).entries).toEqual([])
 expect((await p.info('/team/a',signal())).bytes).toBe(1)
 await p.createFolder('/team','new',signal())
 await createDsmFileStation({webApi}).checkPermission('/team',signal())
 expect(fetch).not.toHaveBeenCalled()
 const asked=Object.fromEntries(station.seen.map(s=>[`${s.method}`,s]))
 expect(asked.list).toMatchObject({folder_path:'/team',additional:['size'],offset:0,limit:100,sort_by:'name',sort_direction:'ASC'})
 expect(asked.getinfo).toMatchObject({path:['/team/a'],additional:['size']})
 expect(asked.create).toMatchObject({folder_path:['/team'],name:['new'],force_parent:false})
 expect(asked.write).toMatchObject({path:'/team',overwrite:false,size:0})
 p.dispose()
})

test('on DSM 7.4 direct WebAPI calls carry the session token in X-SYNO-TOKEN, never in the body', async()=>{
 const {requests,station}=dsmFetch()
 const p=createNasProvider({...admitted,files:{token:()=>TOKEN}})
 expect((await p.shares(0,signal())).entries[0]?.path).toBe('/team')
 await p.list('/team',{},signal())
 expect((await p.info('/team/a',signal())).bytes).toBe(1)
 await p.createFolder('/team','new',signal())
 const t=createFileTransfers({token:()=>TOKEN})
 await t.upload('/team',new File(['a'],'a.txt'),signal())
 expect(await readDsmPorts({files:{token:()=>TOKEN}})).toHaveLength(1)
 expect(requests.length).toBe(6)
 for(const r of requests){expect(r.bodyToken).toBe(false);expect(r.header).toBe(TOKEN);expect(r.query.has('SynoToken')).toBe(false)}
 expect(station.seen.map(s=>s.method)).toEqual(['list_share','list','getinfo','create','upload','load'])
 expect(station.seen[2]).toMatchObject({path:['/team/a']})
 t.dispose();p.dispose()
})

test('on DSM 7.4 a portal page without a desktop token reads one from login.cgi and sends it as a header', async()=>{
 const {requests}=dsmFetch()
 const files=createDsmFileStation({token:()=>undefined})
 await files.shares(0,signal())
 expect(requests.map(r=>[new URL(r.url,'https://nas.invalid').pathname,r.bodyToken,r.header])).toEqual([
  ['/webman/login.cgi',false,undefined],['/webapi/entry.cgi',false,TOKEN],
 ])
})
