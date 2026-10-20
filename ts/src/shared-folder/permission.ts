import type {CallOptions,Context} from '../context'
import {format} from '../i18n'
import {createSharedFolderAccess} from './access'
import {sharedFolderMessages} from './i18n'

/** The public details of shared_folder_permission, emitted by Go PermissionError. */
export interface SharedFolderPermissionIssue {
 account:string
 share:string
 required:'read'|'write'
}
export interface SharedFolderPermission {
 messages:Record<string,string>
 message(issue:SharedFolderPermissionIssue):string
 /** Starts at most one background status request for this App instance. */
 check():Promise<void>
 /** Call in an existing catch block; true means the permission dialog handled it. */
 handle(error:unknown):boolean
 /** The App's normal call, with permission failures sent to the same dialog. Still rejects. */
 call:Context['call']
 subscribe(listener:(issues:readonly SharedFolderPermissionIssue[])=>void):()=>void
 openControlPanel():void
}
export interface SharedFolderPermissionOptions {
 call:Context['call']
 locale?:string
 openControlPanel?:()=>void
}

function issueFrom(error:unknown):SharedFolderPermissionIssue|undefined {
 if(!error||typeof error!=='object'||!('code' in error)||error.code!=='shared_folder_permission'||!('details' in error))return
 const d=error.details
 if(!d||typeof d!=='object'||!('account' in d)||!('share' in d)||!('required' in d))return
 if(typeof d.account!=='string'||!d.account||typeof d.share!=='string'||!d.share||(d.required!=='read'&&d.required!=='write'))return
 return {account:d.account,share:d.share,required:d.required}
}

/** Shared instruction text for startup checks, I/O failures and custom App UI. */
export function sharedFolderPermissionMessage(issue:SharedFolderPermissionIssue,locale='en'):string {
 const m=sharedFolderMessages(locale)
 return format(m.steps!,{account:issue.account,folder:issue.share,access:m[issue.required]!},locale)
}

/** Create once per open App. No timers, retries, identity changes or grant calls. */
export function createSharedFolderPermission(options:SharedFolderPermissionOptions):SharedFolderPermission {
 const locale=options.locale??'en'
 const shared=createSharedFolderAccess({call:options.call,locale,isAdmin:()=>false})
 const listeners=new Set<(issues:readonly SharedFolderPermissionIssue[])=>void>()
 let issues:SharedFolderPermissionIssue[]=[],checked:Promise<void>|undefined
 function publish(next:SharedFolderPermissionIssue[]) {
  const unique=new Map<string,SharedFolderPermissionIssue>()
  for(const issue of next)unique.set(JSON.stringify([issue.account,issue.share,issue.required]),issue)
  issues=[...unique.values()]
  for(const listener of listeners)listener(issues)
 }
 const permission:SharedFolderPermission={
  messages:sharedFolderMessages(locale),
  message:issue=>sharedFolderPermissionMessage(issue,locale),
  check(){
   return checked??=shared.status().then(status=>{
    const pending=status.folders.filter(f=>f.state==='pending'&&f.required!=='none').map(f=>({account:status.account,share:f.name,required:f.required as 'read'|'write'}))
    if(pending.length)publish([...pending,...issues])
   })
  },
  handle(error){
   const issue=issueFrom(error)
   if(!issue)return false
   publish([...issues,issue]);return true
  },
  async call<T=unknown>(method:string,params?:Record<string,unknown>,callOptions?:CallOptions):Promise<T>{
   try{return await options.call<T>(method,params,callOptions)}
   catch(error){permission.handle(error);throw error}
  },
  subscribe(listener){
   listeners.add(listener)
   if(issues.length)listener(issues)
   return ()=>{listeners.delete(listener)}
  },
  openControlPanel:()=>options.openControlPanel?.(),
 }
 return permission
}
