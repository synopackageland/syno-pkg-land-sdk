import {expect,test} from 'vitest'
import {createPortalAccess,type PortalOptions} from './access'

// The DSM window's portal runtime (cli portal-window.ts), for an App's own DSM mount.
const APP='SYNO.SDS.HelloGo.Instance'
type Sent={api:string;method:string;version:number;params:Record<string,unknown>}
const info={'SYNO.Core.AppPortal':{minVersion:1,maxVersion:2}}
function dsm(item:Record<string,unknown>|Error,setError?:number){
 return (sent:Sent)=>{
  if(sent.api==='SYNO.API.Info')return info
  if(sent.method==='get'){if(item instanceof Error)throw {code:'dsm_4101'};return item}
  if(setError)throw {code:`dsm_${setError}`}
  return {}
 }
}
function access(o:{admin:boolean;entry?:boolean;dsm?:(sent:Sent)=>unknown;backend?:(method:string)=>unknown}&Partial<PortalOptions>){
 const sent:Sent[]=[],calls:string[]=[],launched:unknown[]=[]
 const portal=createPortalAccess({
  appId:APP,defaultAlias:'hellogo',locale:'en',entry:o.entry,
  isAdmin:()=>o.admin,
  send:async(api,method,version,params)=>{const request={api,method,version,params};sent.push(request);return o.dsm!(request)},
  call:async method=>{calls.push(method);return o.backend!(method)},
  launch:(...args)=>{launched.push(args)},
  location:{protocol:'https:',host:'nas.example:5001'},
  ...(o.locale?{locale:o.locale}:{}),
 })
 return {portal,sent,calls,launched}
}
const code=(expected:string)=>expect.objectContaining({code:expected})

test("an administrator manages the alias with DSM's own API, prefilled with the installed default",async()=>{
 const {portal,sent,calls}=access({admin:true,dsm:dsm({id:APP,additional:{default_setting:{alias:'my-app'}}})})
 expect(await portal.status()).toEqual({state:'inactive',canManage:true,controlPanel:true,defaultAlias:'my-app'})
 expect(sent.slice(0,2)).toEqual([
  {api:'SYNO.API.Info',method:'query',version:1,params:{query:'SYNO.Core.AppPortal'}},
  {api:'SYNO.Core.AppPortal',method:'get',version:2,params:{id:APP,additional:['default_setting']}},
 ])
 await portal.apply('my-app')
 expect(sent.find(s=>s.method==='set')).toEqual({api:'SYNO.Core.AppPortal',method:'set',version:2,params:{id:APP,alias:'my-app'}})
 await portal.disable()
 expect(sent.filter(s=>s.method==='set').at(-1)!.params).toEqual({id:APP,alias:null})
 expect(calls).toEqual([])
})

test('an active alias becomes a link on the NAS address the user is on',async()=>{
 const {portal}=access({admin:true,dsm:dsm({id:APP,alias:'my-app',additional:{default_setting:{alias:'hellogo'}}})})
 expect(await portal.status()).toEqual({state:'active',alias:'my-app',url:'https://nas.example:5001/my-app/',canManage:true,controlPanel:true,defaultAlias:'hellogo'})
})

test("DSM's alias errors come back as portal error codes",async()=>{
 for(const [dsmCode,expected] of [[4103,'portal_alias_in_use'],[4102,'portal_alias_reserved'],[4100,'portal_alias_invalid'],[105,'admin_required'],[119,'portal_unavailable']] as const)
  await expect(access({admin:true,dsm:dsm({id:APP},dsmCode)}).portal.apply('my-app'),String(dsmCode)).rejects.toEqual(code(expected))
})

test('an alias the SDK refuses never reaches DSM',async()=>{
 for(const alias of ['','Bad','-a','a/b','webman','file',7,null]){
  const {portal,sent}=access({admin:true,dsm:dsm({id:APP})})
  await expect(portal.apply(alias as string),String(alias)).rejects.toEqual({code:expect.stringMatching(/^portal_alias_(invalid|reserved)$/)})
  expect(sent.filter(s=>s.method==='set')).toEqual([])
 }
})

test("without DSM's portal API or registration an administrator gets only the Control Panel shortcut",async()=>{
 for(const handler of [()=>({}),dsm(new Error('unregistered')),dsm({id:'SYNO.SDS.Other.Instance'})]){
  const {portal,launched}=access({admin:true,dsm:handler})
  expect(await portal.status()).toEqual({state:'unavailable',canManage:false,controlPanel:true,defaultAlias:'hellogo'})
  portal.openControlPanel()
  expect(launched).toEqual([['SYNO.SDS.AdminCenter.Application',{fn:'SYNO.SDS.AdminCenter.Login.Main',tab:'applications'}]])
 }
})

test('an ordinary user reads the alias only through the package backend and cannot manage it',async()=>{
 const active=access({admin:false,backend:()=>({state:'active',alias:'my-app'})})
 expect(await active.portal.status()).toEqual({state:'active',alias:'my-app',url:'https://nas.example:5001/my-app/',canManage:false,controlPanel:false,defaultAlias:'hellogo'})
 expect(active.calls).toEqual(['portal.status'])
 expect(active.sent).toEqual([])
 await expect(active.portal.apply('other')).rejects.toEqual(code('admin_required'))
 for(const backend of [()=>{throw {code:'unknown_method'}},()=>({state:'active',alias:'../x'}),()=>({state:'inactive'}),()=>null]){
  const status=await access({admin:false,backend}).portal.status()
  expect(status.url).toBeUndefined()
  expect(status.canManage).toBe(false)
 }
})

test("the panel text is in the given DSM language",()=>{
 expect(access({admin:true,locale:'cht'}).portal.messages.askAdmin).toBe('請管理員設定')
 expect(access({admin:true}).portal.messages.askAdmin).toBe('Ask an administrator to set this up')
})

test('register and unregister exist only with entry (below DSM 7.2)',()=>{
 expect(access({admin:true}).portal.register).toBeUndefined()
 expect(access({admin:true}).portal.unregister).toBeUndefined()
 expect(access({admin:true,entry:true}).portal.register).toBeTypeOf('function')
 expect(access({admin:true,entry:true}).portal.unregister).toBeTypeOf('function')
})

// Below DSM 7.2 the executor registers the entry; the backend's portal.status says which state it is in.
function entryAccess(o:{admin:boolean;entry:unknown;state?:string;alias?:string;dsm?:(sent:Sent)=>unknown;change?:()=>void}){
 let entry=o.entry
 return access({admin:o.admin,entry:true,dsm:o.dsm,backend:method=>{
  if(method!=='portal.status'){o.change?.();entry=method==='portal.register'?'registered':'unregistered';return {state:'inactive',entry}}
  return {state:o.state??'inactive',...(o.alias?{alias:o.alias}:{}),entry}
 }})
}

test('below DSM 7.2 an administrator registers the missing entry before any alias',async()=>{
 const {portal,sent,calls}=entryAccess({admin:true,entry:'unregistered',dsm:dsm({id:APP})})
 expect(await portal.status()).toEqual({state:'inactive',canManage:true,controlPanel:true,defaultAlias:'hellogo',entry:'unregistered'})
 expect(sent).toEqual([])
 expect((await portal.register!()).entry).toBe('registered')
 expect(calls).toEqual(['portal.status','portal.register','portal.status'])
})

test("below DSM 7.2 a registered entry is managed with DSM's own API and can be removed",async()=>{
 const {portal,calls}=entryAccess({admin:true,entry:'registered',dsm:dsm({id:APP,alias:'my-app',additional:{default_setting:{alias:'hellogo'}}})})
 expect(await portal.status()).toEqual({state:'active',alias:'my-app',url:'https://nas.example:5001/my-app/',canManage:true,controlPanel:true,defaultAlias:'hellogo',entry:'registered'})
 await portal.unregister!()
 expect(calls.slice(-2)).toEqual(['portal.unregister','portal.status'])
})

test('below DSM 7.2 a half-removed entry asks the administrator to remove it again',async()=>{
 const {portal,sent}=entryAccess({admin:true,entry:'cleanup-required',state:'active',alias:'my-app',dsm:dsm({id:APP})})
 expect(await portal.status()).toEqual({state:'active',alias:'my-app',url:'https://nas.example:5001/my-app/',canManage:true,controlPanel:true,defaultAlias:'hellogo',entry:'cleanup-required'})
 expect(sent).toEqual([])
})

test('below DSM 7.2 an unknown entry or failed change reads as unavailable or PORTAL_FAILED',async()=>{
 for(const entry of [undefined,'bogus'])
  expect(await entryAccess({admin:true,entry,dsm:dsm({id:APP})}).portal.status()).toEqual({state:'unavailable',canManage:false,controlPanel:true,defaultAlias:'hellogo'})
 for(const [failure,expected] of [['PORTAL_FAILED','PORTAL_FAILED'],['ADMIN_REQUIRED','admin_required'],['EXECUTOR_UNAVAILABLE','PORTAL_FAILED']] as const)
  await expect(entryAccess({admin:true,entry:'unregistered',change:()=>{throw {code:failure}}}).portal.register!(),failure).rejects.toEqual(code(expected))
 const user=entryAccess({admin:false,entry:'registered',state:'active',alias:'my-app'})
 expect(await user.portal.status()).toEqual({state:'active',alias:'my-app',url:'https://nas.example:5001/my-app/',canManage:false,controlPanel:false,defaultAlias:'hellogo'})
 await expect(user.portal.register!()).rejects.toEqual(code('admin_required'))
 expect(user.calls).toEqual(['portal.status'])
})

// the backend reports cleanup-required while a change runs.
test('a status read during a register or unregister waits for it to end',async()=>{
 for(const fails of [false,true]){
  let finish:()=>void=()=>{},entry='unregistered'
  const calls:string[]=[]
  const portal=createPortalAccess({appId:APP,defaultAlias:'hellogo',locale:'en',entry:true,isAdmin:()=>true,
   send:async(api,method)=>api==='SYNO.API.Info'?info:method==='get'?{id:APP}:{},
   call:async method=>{
    calls.push(method)
    if(method==='portal.status')return {state:'inactive',entry}
    entry='cleanup-required'
    await new Promise<void>(r=>{finish=r})
    entry=fails?'unregistered':'registered'
    if(fails)throw {code:'PORTAL_FAILED'}
    return {}
   },
   launch(){},location:{protocol:'https:',host:'nas'}})
  const change=portal.register!().catch(()=>undefined)
  await Promise.resolve()
  const read=portal.status()
  await new Promise(r=>setTimeout(r,0))
  // The one status read is the access's own, which started before the change.
  expect(calls,String(fails)).toEqual(['portal.register','portal.status'])
  finish()
  expect((await read).entry,String(fails)).toBe(fails?'unregistered':'registered')
  await change
 }
})

// the settings page shows the section as last read (PortalSettings),
// so the access reads the status once it exists and keeps each status it reads.
test('the access reads the status when it is created and keeps the last one it read',async()=>{
 const {portal,sent}=access({admin:true,dsm:dsm({id:APP})})
 expect(portal.lastStatus!()).toBeUndefined()
 await new Promise(r=>setTimeout(r))
 expect(sent.filter(s=>s.method==='get')).toHaveLength(1)
 expect(portal.lastStatus!()).toEqual({state:'inactive',canManage:true,controlPanel:true,defaultAlias:'hellogo'})
 await portal.apply('my-app')
 expect(portal.lastStatus!()?.state).toBe('inactive')
 const hidden=access({admin:true,dsm:dsm({id:'other'})})
 await new Promise(r=>setTimeout(r))
 expect(hidden.portal.lastStatus!()).toEqual({state:'unavailable',canManage:false,controlPanel:true,defaultAlias:'hellogo'})
})
