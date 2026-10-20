import {expect,test} from 'vitest'
import {createSharedFolderAccess,sharedFolderAccessFor,type SharedFolderOptions} from './access'

type Sent={api:string;method:string;version:number;params:Record<string,unknown>}
const folder=(over:Record<string,unknown>={})=>({key:'media',name:'Media',path:'/volume1/Media',exists:true,required:'write',granted:'read',state:'pending',...over})
const reply=(...folders:Array<Record<string,unknown>>)=>({account:'OpenList',folders})
function access(o:{admin?:boolean;noSend?:boolean;backend:Array<unknown>;dsm?:(sent:Sent)=>unknown}&Partial<SharedFolderOptions>){
 const sent:Sent[]=[],calls:string[]=[],launched:unknown[]=[]
 const backend=[...o.backend]
 const shared=createSharedFolderAccess({
  locale:'en',isAdmin:()=>o.admin??true,
  call:async method=>{calls.push(method);const next=backend.length>1?backend.shift():backend[0];if(next instanceof Error)throw {code:'internal'};return next},
  ...(o.noSend?{}:{send:async(api,method,version,params)=>{const request={api,method,version,params};sent.push(request);return o.dsm?.(request)??{}}}),
  launch:(...args)=>{launched.push(args)},
 })
 return {shared,sent,calls,launched}
}
const code=(expected:string)=>expect.objectContaining({code:expected})

test('status is what the backend measured for the package account, with pending derived',async()=>{
 const {shared,calls}=access({backend:[reply(folder(),folder({key:'b',name:'B',state:'ready',granted:'write'}))]})
 const status=await shared.status()
 expect(calls).toEqual(['sharedfolder.status'])
 expect(status.account).toBe('OpenList')
 expect(status.pending).toBe(true)
 expect(status.folders.map(f=>[f.key,f.state,f.required,f.granted])).toEqual([['media','pending','write','read'],['b','ready','write','write']])
 expect(shared.lastStatus!()).toBe(status)
})

test('an all-ready answer is not pending',async()=>{
 const {shared}=access({backend:[reply(folder({state:'ready',granted:'write'}))]})
 expect((await shared.status()).pending).toBe(false)
})

test.each([
 ['not an object',null],['no account',{folders:[]}],['folders not a list',{account:'x',folders:{}}],
 ['unknown state',reply(folder({state:'weird'}))],['unknown level',reply(folder({granted:'owner'}))],['no name',reply(folder({name:undefined}))],
])('an answer this reader does not fully understand is unavailable: %s',async(_,data)=>{
 const {shared}=access({backend:[data]})
 await expect(shared.status()).rejects.toEqual(code('shared_folder_unavailable'))
})

test('a failing backend is unavailable and never reaches DSM',async()=>{
 const {shared,sent}=access({backend:[new Error('x')]})
 await expect(shared.status()).rejects.toEqual(code('shared_folder_unavailable'))
 expect(sent).toEqual([])
})

test("an administrator's grant sets the package account on the share in DSM's session, then trusts only the backend's readback",async()=>{
 const {shared,sent}=access({backend:[reply(folder()),reply(folder({state:'ready',granted:'write'}))]})
 expect(shared.canGrant()).toBe(true)
 const status=await shared.grant('media')
 expect(status.pending).toBe(false)
 const set=sent.find(s=>s.method==='set')!
 expect(set).toEqual({api:'SYNO.Core.Share.Permission',method:'set',version:1,params:{name:'Media',user_group_type:'system',permissions:[{name:'OpenList',is_readonly:false,is_writable:true,is_deny:false,is_custom:false}]}})
})

test('a read-only requirement grants read, never write',async()=>{
 const {shared,sent}=access({backend:[reply(folder({required:'read',granted:'none'})),reply(folder({required:'read',granted:'read',state:'ready'}))]})
 await shared.grant('media')
 expect(sent.find(s=>s.method==='set')!.params.permissions).toEqual([{name:'OpenList',is_readonly:true,is_writable:false,is_deny:false,is_custom:false}])
})

test('DSM accepting the change is not enough: the account must then really have the access',async()=>{
 const {shared}=access({backend:[reply(folder())]})
 await expect(shared.grant('media')).rejects.toEqual(code('shared_folder_grant_unverified'))
})

test("DSM's refusals map to codes, and 105 means not an administrator",async()=>{
 const refuse=(dsm:string)=>access({backend:[reply(folder())],dsm:s=>{if(s.method==='set')throw {code:dsm};return {}}}).shared.grant('media')
 await expect(refuse('dsm_105')).rejects.toEqual(code('admin_required'))
 await expect(refuse('dsm_3300')).rejects.toEqual(code('shared_folder_grant_failed'))
})

test('a custom (advanced) ACL row for the account stops the grant before DSM changes anything',async()=>{
 const {shared,sent}=access({backend:[reply(folder())],dsm:s=>s.method==='list'?{items:[{name:'other',is_custom:false},{name:'OpenList',is_custom:true}]}:{}})
 await expect(shared.grant('media')).rejects.toEqual(code('shared_folder_custom_acl'))
 expect(sent.some(s=>s.method==='set')).toBe(false)
})

test('a list DSM will not answer does not stop the grant',async()=>{
 const {shared}=access({backend:[reply(folder()),reply(folder({state:'ready',granted:'write'}))],dsm:s=>{if(s.method==='list')throw {code:'dsm_119'};return {}}})
 expect((await shared.grant('media')).pending).toBe(false)
})

test('no grant without an administrator, a DSM channel, or a folder that only a permission can fix',async()=>{
 await expect(access({admin:false,backend:[reply(folder())]}).shared.grant('media')).rejects.toEqual(code('admin_required'))
 const noSend=access({noSend:true,backend:[reply(folder())]})
 expect(noSend.shared.canGrant()).toBe(false)
 await expect(noSend.shared.grant('media')).rejects.toEqual(code('admin_required'))
 for(const state of ['missing','ambiguous']){
  const {shared,sent}=access({backend:[reply(folder({state,exists:state==='ambiguous',granted:'none'}))]})
  await expect(shared.grant('media')).rejects.toEqual(code('shared_folder_unavailable'))
  expect(sent).toEqual([])
 }
 await expect(access({backend:[reply(folder())]}).shared.grant('nope')).rejects.toEqual(code('shared_folder_unavailable'))
})

test('a folder that is already ready needs no change',async()=>{
 const {shared,sent}=access({backend:[reply(folder({state:'ready',granted:'write'}))]})
 await shared.grant('media')
 expect(sent).toEqual([])
})

test('the guided path opens Control Panel Shared Folder',()=>{
 const {shared,launched}=access({backend:[reply()]})
 shared.openControlPanel()
 expect(launched).toEqual([['SYNO.SDS.AdminCenter.Application',{fn:'SYNO.SDS.AdminCenter.Share.Main'}]])
})

test('messages follow the DSM language',()=>{
 expect(createSharedFolderAccess({locale:'cht',isAdmin:()=>false,call:async()=>({})}).messages.title).toBe('共用資料夾')
})

test('the context helper takes the call, DSM channel, admin flag and launcher from the App context',async()=>{
 const opened:string[]=[],sent:string[]=[]
 const shared=sharedFolderAccessFor({
  call:(async(method:string)=>{sent.push(method);return reply(folder({state:'ready',granted:'write'}))}) as never,
  webApi:async()=>({}),user:{name:'a',isAdmin:true},locale:'en',
  host:{openApp:(app:string)=>{opened.push(app);return {status:'requested'}}} as never,
 })
 expect(shared.canGrant()).toBe(true)
 expect((await shared.status()).account).toBe('OpenList')
 shared.openControlPanel()
 expect(sent).toEqual(['sharedfolder.status'])
 expect(opened).toEqual(['SYNO.SDS.AdminCenter.Application'])
 expect(sharedFolderAccessFor({call:(async()=>({})) as never,user:{name:'a',isAdmin:false},locale:'en',host:{} as never}).canGrant()).toBe(false)
})
