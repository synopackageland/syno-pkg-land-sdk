import type {Context,SharedFolderAccess,SharedFolderItem,SharedFolderStatus} from '../context'
import {sharedFolderMessages} from './i18n'

// Shared Folder access for the package account: what the backend's
// sharedfolder.status (Go sharedfolder.Config.Status) measured as that
// account, and, for an administrator, DSM's own SYNO.Core.Share.Permission in
// the administrator's session. The App never uses root for this. The
// backend's answer is the only proof of a grant: DSM's reply is not trusted
// until the backend reads the new access.
export interface SharedFolderOptions {
 // The package backend; its sharedfolder.status answers {account, folders}.
 call(method:'sharedfolder.status',params:Record<string,never>):Promise<unknown>
 // DSM's WebAPI in the signed-in user's session; a DSM error rejects {code: 'dsm_<code>'}.
 // Without it only the guided steps are offered.
 send?(api:string,method:string,version:number,params:Record<string,unknown>):Promise<unknown>
 isAdmin():boolean
 // Opens a DSM app (Control Panel); without it openControlPanel does nothing.
 launch?(app:string,params:Record<string,string>):void
 // DSM's language code or a BCP-47 tag; picks the panel text.
 locale:string
}

const LEVELS=['none','read','write'] as const
const STATES=['ready','pending','missing','ambiguous'] as const
const codeOf=(error:unknown)=>(error as {code?:string}|null)?.code
const record=(value:unknown):Record<string,unknown>|undefined=>value&&typeof value==='object'&&!Array.isArray(value)?value as Record<string,unknown>:undefined

function item(value:unknown):SharedFolderItem|undefined{
 const v=record(value)
 if(!v||typeof v.key!=='string'||typeof v.name!=='string')return undefined
 const required=LEVELS.find(level=>level===v.required),granted=LEVELS.find(level=>level===v.granted),state=STATES.find(s=>s===v.state)
 if(!required||!granted||!state)return undefined
 const out:SharedFolderItem={key:v.key,name:v.name,exists:v.exists===true,required,granted,state}
 if(typeof v.path==='string')out.path=v.path
 if(typeof v.error==='string')out.error=v.error
 return out
}

// Anything the backend says that this reader does not fully understand is unavailable.
function parse(data:unknown):SharedFolderStatus|undefined{
 const v=record(data)
 if(!v||typeof v.account!=='string'||!Array.isArray(v.folders))return undefined
 const folders=v.folders.map(item)
 return folders.every(f=>f)?{account:v.account,folders:folders as SharedFolderItem[],pending:folders.some(f=>f!.state==='pending')}:undefined
}

type Row={name?:unknown;is_custom?:unknown}

/** The SDK's Shared Folder access for a context that carries the pieces it needs. */
export function createSharedFolderAccess(options:SharedFolderOptions):SharedFolderAccess{
 let last:SharedFolderStatus|undefined
 async function read():Promise<SharedFolderStatus>{
  let data:unknown
  try{data=await options.call('sharedfolder.status',{})}catch{throw {code:'shared_folder_unavailable'}}
  const status=parse(data)
  if(!status)throw {code:'shared_folder_unavailable'}
  return last=status
 }
 const access:SharedFolderAccess={
  messages:sharedFolderMessages(options.locale),
  status:read,
  lastStatus:()=>last,
  canGrant:()=>options.isAdmin()&&options.send!==undefined,
  async grant(key){
   if(!options.isAdmin()||!options.send)throw {code:'admin_required'}
   const status=await read()
   const folder=status.folders.find(f=>f.key===key)
   if(!folder||folder.state==='missing'||folder.state==='ambiguous')throw {code:'shared_folder_unavailable'}
   if(folder.state==='ready')return status
   if(folder.required==='none')throw {code:'shared_folder_unavailable'}
   const write=folder.required==='write'
   // An advanced (Windows) ACL overrides the share permission and DSM cannot show it as a plain
   // row: refuse before changing anything. A list DSM will not answer does not stop the grant.
   try{
    const listed=record(await options.send('SYNO.Core.Share.Permission','list',1,{name:folder.name,user_group_type:'system',offset:0,limit:1000,with_inherit:false,is_unite_permission:false}))
    const rows=Array.isArray(listed?.items)?listed.items as Row[]:[]
    if(rows.some(row=>row?.name===status.account&&row.is_custom===true))throw {code:'shared_folder_custom_acl'}
   }catch(error){if(codeOf(error)==='shared_folder_custom_acl')throw error}
   try{
    await options.send('SYNO.Core.Share.Permission','set',1,{name:folder.name,user_group_type:'system',permissions:[{name:status.account,is_readonly:!write,is_writable:write,is_deny:false,is_custom:false}]})
   }catch(error){throw {code:codeOf(error)==='dsm_105'?'admin_required':'shared_folder_grant_failed'}}
   const next=await read()
   if(next.folders.find(f=>f.key===key)?.state!=='ready')throw {code:'shared_folder_grant_unverified'}
   return next
  },
  openControlPanel(){options.launch?.('SYNO.SDS.AdminCenter.Application',{fn:'SYNO.SDS.AdminCenter.Share.Main'})},
 }
 return access
}

/** The access for an App context: the backend call, DSM's session channel when the window offers it, and the window's launcher. */
export function sharedFolderAccessFor(ctx:Pick<Context,'call'|'webApi'|'user'|'locale'|'host'>):SharedFolderAccess{
 return createSharedFolderAccess({
  call:(method,params)=>ctx.call(method,params),
  send:ctx.webApi,
  isAdmin:()=>ctx.user?.isAdmin===true,
  launch:(app)=>{ctx.host.openApp(app)},
  locale:ctx.locale,
 })
}
