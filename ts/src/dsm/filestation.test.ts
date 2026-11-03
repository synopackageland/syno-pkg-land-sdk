// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createNasProvider} from '../nas/provider'
import {createFileTransfers} from '../files/transfers'
import {createDsmFileStation} from './filestation'

afterEach(()=>vi.unstubAllGlobals())
const signal=()=>new AbortController().signal
test('direct File Station reads tolerate slow NAS responses and honor a shorter override',async()=>{
 vi.useFakeTimers()
 try{
  const fetch=vi.fn((_url:unknown,init?:RequestInit)=>new Promise((_resolve,reject)=>{
   init!.signal!.addEventListener('abort',()=>reject(new Error('aborted')),{once:true})
  }))
  vi.stubGlobal('fetch',fetch)
  for(const [timeout,wait] of [[undefined,302000],[1000,1000]] as const){
   const files=createDsmFileStation({token:()=> 'token',timeout}),seen=vi.fn()
   const pending=files.shares(0,signal()).catch(seen)
   await vi.advanceTimersByTimeAsync(wait-1)
   expect(seen).not.toHaveBeenCalled()
   await vi.advanceTimersByTimeAsync(1)
   await pending
   expect(seen).toHaveBeenCalledWith({code:'timeout'})
  }
 }finally{vi.useRealTimers()}
})
function browser(){
 const fetch=vi.fn(async(_url:unknown,init?:RequestInit)=>{
  const form=init?.body as URLSearchParams
  const method=form instanceof URLSearchParams?form.get('method'):''
  const data=method==='list_share'?{shares:[{name:'team',path:'/team',isdir:true}],offset:0,total:1}:method==='getinfo'?{files:[{name:'a',path:'/team/a',isdir:false,additional:{size:1}}]}:method==='list'?{files:[],offset:0,total:0}:{}
  return new Response(JSON.stringify({success:true,data}))
 })
 vi.stubGlobal('fetch',fetch)
 const call=vi.fn(async(method:string)=>{if(method!=='nas.session.status')throw Error('App must not relay File Station');return {mode:'browser-session'}})
 return {fetch,call,ctx:{call:call as any,files:{endpoint:'https://untrusted.invalid/relay',token:()=> 'browser-token'}}}
}
test('NAS shares/list/info/create use a fixed same-origin DSM endpoint after App admission',async()=>{
 const {fetch,call,ctx}=browser(),p=createNasProvider(ctx)
 expect((await p.shares(0,signal())).entries[0]?.path).toBe('/team')
 await p.list('/team',{},signal())
 expect((await p.info('/team/a',signal())).bytes).toBe(1)
 await p.createFolder('/team','new',signal())
 expect(call).toHaveBeenCalledTimes(4)
 for(const [url,init] of fetch.mock.calls){
  expect(url).toBe('/webapi/entry.cgi')
  expect(init).toMatchObject({mode:'same-origin',credentials:'same-origin',redirect:'error',cache:'no-store'})
  expect((init!.body as URLSearchParams).has('SynoToken')).toBe(false)
  expect(new Headers(init!.headers).get('X-SYNO-TOKEN')).toBe('browser-token')
 }
 expect(fetch.mock.calls.map(([,init])=>(init!.body as URLSearchParams).get('method'))).toEqual(['list_share','list','getinfo','create'])
 p.dispose()
})
test('App admission refusal makes zero DSM requests',async()=>{
 const {fetch,ctx}=browser()
 ctx.call=vi.fn(async()=>{throw {code:'app_privilege_denied'}})
 const p=createNasProvider(ctx)
 await expect(p.shares(0,signal())).rejects.toEqual({code:'app_privilege_denied'})
 expect(fetch).not.toHaveBeenCalled()
 p.dispose()
})
test('NAS JSON requests use ctx.webApi after App admission without reading a token or fetching',async()=>{
 const {fetch,ctx}=browser(),token=vi.fn(()=>{throw Error('window channel owns the session')})
 const webApi=vi.fn(async(_api:string,method:string)=>method==='list_share'?{shares:[{name:'team',path:'/team',isdir:true}],offset:0,total:1}:method==='getinfo'?{files:[{name:'a',path:'/team/a',isdir:false,additional:{size:1}}]}:method==='list'?{files:[],offset:0,total:0}:{})
 const p=createNasProvider({...ctx,files:{token},webApi})
 expect((await p.shares(0,signal())).entries[0]?.path).toBe('/team')
 await p.list('/team',{},signal())
 expect((await p.info('/team/a',signal())).bytes).toBe(1)
 await p.createFolder('/team','new',signal())
 expect(webApi.mock.calls.map(([api,method])=>[api,method])).toEqual([
  ['SYNO.FileStation.List','list_share'],['SYNO.FileStation.List','list'],
  ['SYNO.FileStation.List','getinfo'],['SYNO.FileStation.CreateFolder','create'],
 ])
 expect(ctx.call).toHaveBeenCalledTimes(4)
 expect(fetch).not.toHaveBeenCalled();expect(token).not.toHaveBeenCalled()
 p.dispose()
})
test('window WebAPI refusals retain File Station errors without a token fallback',async()=>{
 const fetch=vi.fn();vi.stubGlobal('fetch',fetch)
 for(const [code,expected] of [[105,'denied'],[119,'session_required']] as const){
  const webApi=vi.fn(async()=>{throw {code:`dsm_${code}`}})
  const files=createDsmFileStation({webApi})
  await expect(files.shares(0,signal())).rejects.toEqual({code:expected})
  expect(webApi).toHaveBeenCalledOnce()
 }
 expect(fetch).not.toHaveBeenCalled()
})
test('pending window requests honor cancellation and deadlines without replaying mutations',async()=>{
 vi.useFakeTimers()
 try{
  const webApi=vi.fn(()=>new Promise<never>(()=>{}))
  const files=createDsmFileStation({webApi,timeout:1000})
  const read=files.shares(0,signal()).catch(e=>e)
  await vi.advanceTimersByTimeAsync(1000)
  expect(await read).toEqual({code:'timeout'})
  const controller=new AbortController()
  const write=files.createFolder('/team','new',controller.signal).catch(e=>e)
  controller.abort()
  expect(await write).toEqual({code:'commit-uncertain'})
  expect(webApi).toHaveBeenCalledTimes(2)
  const aborted=new AbortController();aborted.abort()
  await expect(files.shares(0,aborted.signal)).rejects.toEqual({code:'canceled'})
  expect(webApi).toHaveBeenCalledTimes(2)
 }finally{vi.useRealTimers()}
})
test('upload is a DSM multipart request, never an App URL or an octet-stream relay',async()=>{
 const {fetch,ctx}=browser(),t=createFileTransfers(ctx.files),file=new File(['a'],'a')
 await t.upload('/team',file,signal())
 expect(fetch).toHaveBeenCalledWith('/webapi/entry.cgi',expect.objectContaining({mode:'same-origin',redirect:'error',credentials:'same-origin'}))
 const body=fetch.mock.calls[0]![1]!.body as FormData
 expect(body).toBeInstanceOf(FormData)
 expect(body.get('api')).toBe('SYNO.FileStation.Upload')
 expect(body.get('file')).toBe(file)
 expect(body.has('SynoToken')).toBe(false)
 expect(new Headers(fetch.mock.calls[0]![1]!.headers).get('X-SYNO-TOKEN')).toBe('browser-token')
 t.dispose()
})
test('portal token bootstrap is same-origin, shared by concurrent reads and never uses an App URL',async()=>{
 const fetch=vi.fn(async(url:string)=>new Response(JSON.stringify(url.startsWith('/webman/')?{success:true,SynoToken:'portal-token'}:{success:true,data:{shares:[],total:0,offset:0}})))
 vi.stubGlobal('fetch',fetch)
 const files=createDsmFileStation({token:()=>undefined})
 await Promise.all([files.shares(0,signal()),files.shares(0,signal())])
 expect(fetch.mock.calls.filter(([url])=>url.startsWith('/webman/'))).toHaveLength(1)
 expect(fetch.mock.calls.map(([url])=>url)).toEqual(['/webman/login.cgi?enable_syno_token=yes','/webapi/entry.cgi','/webapi/entry.cgi'])
})
test('current write permission must explicitly be non-skip; metadata and targets remain bounded',async()=>{
 const fetch=vi.fn(async()=>new Response(JSON.stringify({success:true,data:{blSkip:false}})))
 vi.stubGlobal('fetch',fetch)
 const files=createDsmFileStation({token:()=> 'token'})
 await files.checkPermission('/team',signal());await files.checkPermission('/team',signal())
 const forms=fetch.mock.calls.map(args=>(args as unknown as [string,RequestInit])[1].body as URLSearchParams)
 expect(forms[0]!.get('filename')).not.toBe(forms[1]!.get('filename'))
 expect(forms[0]!.get('filename')).toMatch(/^\.synopkgland-permission-probe-[a-f0-9]{32}$/)
 vi.stubGlobal('fetch',vi.fn(async()=>new Response(JSON.stringify({success:true,data:{blSkip:true}}))))
 await expect(files.checkPermission('/team',signal())).rejects.toEqual({code:'denied'})
 vi.stubGlobal('fetch',vi.fn(async()=>new Response(' '.repeat(1024*1024+1))))
 await expect(files.shares(0,signal())).rejects.toEqual({code:'unavailable'})
 const untouched=vi.fn();vi.stubGlobal('fetch',untouched)
 await expect(files.info('/team/../etc',signal())).rejects.toEqual({code:'invalid-request'})
 await expect(files.list('/team',{limit:101},signal())).rejects.toEqual({code:'invalid-request'})
 expect(untouched).not.toHaveBeenCalled()
})
