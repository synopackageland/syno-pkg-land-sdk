import type {PortalAccess,PortalStatus} from '../context'
import {portalMessages} from './i18n'
import {controlPanelTarget} from '../dsm/control-panel'

// The DSM window's portal runtime (cli portal-window.ts) for an App that mounts
// itself in DSM. An administrator reads and sets the alias with DSM's own
// SYNO.Core.AppPortal in their own session; DSM refuses everyone else (105).
// An ordinary user reads the alias only through the backend's portal.status.
// Anything unexpected reads as unavailable. With entry (below DSM 7.2) the
// backend's portal.status also reports the executor's entry, and
// administrators get register and unregister.
export interface PortalOptions {
 // The App's DSM instance ID, as DSM lists it in Application Portal.
 appId: string
 defaultAlias: string
 // DSM's language code or a BCP-47 tag; picks the panel text.
 locale: string
 location: {protocol: string; host: string}
 // The package backend; a refusal rejects {code}.
 call(method: 'portal.status' | 'portal.register' | 'portal.unregister', params: Record<string, never>): Promise<unknown>
 // DSM's WebAPI in the signed-in user's session; a DSM error rejects {code: 'dsm_<code>'}.
 send(api: string, method: string, version: number, params: Record<string, unknown>): Promise<unknown>
 isAdmin(): boolean
 launch(app: string, params: Record<string, string>): void
 entry?: boolean
}

const ALIAS=/^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/
// Names DSM already serves (cli portal/alias.ts, observed on DSM 7.4-90069); DSM stays the authority.
const RESERVED=['dsfile','indexdb','mobile','oauth','scripts','sharing','ssdp','synosdsjslib','synohdpack','webapi','webman',
 'calendar','chatplus','contacts','drive','file','photo','sso','synologymeet']
const LINKABLE=/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/
const ENTRIES=['registered','unregistered','cleanup-required'] as const
const ERRORS:Record<string,string>={dsm_4100:'portal_alias_invalid',dsm_4102:'portal_alias_reserved',dsm_4103:'portal_alias_in_use',dsm_105:'admin_required'}
type Entry=typeof ENTRIES[number]
type Backend={state?:unknown;alias?:unknown;entry?:unknown}|null|undefined
const codeOf=(error:unknown)=>(error as {code?:string}|null)?.code

export function createPortalAccess(options:PortalOptions):PortalAccess{
 // managed: true lets the panel set the alias, false offers only Control Panel, undefined neither.
 function result(state:PortalStatus['state'],alias?:string,managed?:boolean):PortalStatus{
  const out:PortalStatus={state,canManage:managed===true,controlPanel:managed!==undefined,defaultAlias:options.defaultAlias}
  if(state==='active'&&alias){
   out.alias=alias
   out.url=`${options.location.protocol}//${options.location.host}/${encodeURIComponent(alias)}/`
  }
  return out
 }
 function readBackend(data:Backend):PortalStatus{
  if(data?.state==='active'&&typeof data.alias==='string'&&LINKABLE.test(data.alias))return result('active',data.alias)
  return result(data?.state==='inactive'?'inactive':'unavailable')
 }
 async function fromBackend(){
  try{return readBackend(await options.call('portal.status',{}) as Backend)}catch{return result('unavailable')}
 }
 async function fromDsm(){
  try{
   const info=await options.send('SYNO.API.Info','query',1,{query:'SYNO.Core.AppPortal'}) as Record<string,{maxVersion?:number}>|null
   if(!((info?.['SYNO.Core.AppPortal']?.maxVersion??0)>=2))return result('unavailable',undefined,false)
   const item=await options.send('SYNO.Core.AppPortal','get',2,{id:options.appId,additional:['default_setting']}) as {id?:unknown;alias?:unknown;additional?:{default_setting?:{alias?:unknown}}}|null
   if(item?.id!==options.appId)return result('unavailable',undefined,false)
   const alias=typeof item.alias==='string'&&LINKABLE.test(item.alias)?item.alias:''
   const out=result(alias?'active':'inactive',alias,true)
   const fallback=item.additional?.default_setting?.alias
   if(typeof fallback==='string'&&ALIAS.test(fallback))out.defaultAlias=fallback
   return out
  }catch{return result('unavailable',undefined,false)}
 }
 // DSM lists the App only while the executor's entry is registered.
 async function fromEntry():Promise<PortalStatus>{
  let data:Backend
  try{data=await options.call('portal.status',{}) as Backend}
  catch{return options.isAdmin()?result('unavailable',undefined,false):result('unavailable')}
  if(!options.isAdmin())return readBackend(data)
  const entry=ENTRIES.find(value=>value===data?.entry)
  if(!entry)return result('unavailable',undefined,false)
  if(entry==='registered')return {...await fromDsm(),entry}
  const read=readBackend(data)
  return {...result(entry==='unregistered'?'inactive':read.state,read.alias,true),entry}
 }
 // The backend reports cleanup-required while a register or unregister runs,
 // so a status read waits for the change, whatever its result.
 let changing:Promise<unknown>=Promise.resolve()
 // last: the newest status read, which a panel shows before it reads again. A
 // read that runs serves every status() asked for meanwhile, until a change
 // starts; the change reads afresh.
 let last:PortalStatus|undefined,pending:Promise<PortalStatus>|undefined,reads=0
 async function read(){
  const n=++reads
  await changing
  const out=options.entry?await fromEntry():options.isAdmin()?await fromDsm():await fromBackend()
  if(n===reads)last=out
  return out
 }
 function status(){
  return pending??=read().finally(()=>{pending=undefined})
 }
 async function change(method:'portal.register'|'portal.unregister'){
  if(!options.isAdmin())throw {code:'admin_required'}
  const call=options.call(method,{})
  changing=call.catch(()=>{});pending=undefined
  try{await call}
  catch(error){throw {code:codeOf(error)==='ADMIN_REQUIRED'?'admin_required':'PORTAL_FAILED'}}
  return read()
 }
 async function set(alias:string|null){
  if(!options.isAdmin())throw {code:'admin_required'}
  try{await options.send('SYNO.Core.AppPortal','set',2,{id:options.appId,alias})}
  catch(error){throw {code:ERRORS[codeOf(error)??'']??'portal_unavailable'}}
  return read()
 }
 const access:PortalAccess={
  messages:portalMessages(options.locale),
  status,
  lastStatus:()=>last,
  async apply(alias){
   if(typeof alias!=='string'||!ALIAS.test(alias))throw {code:'portal_alias_invalid'}
   if(RESERVED.includes(alias))throw {code:'portal_alias_reserved'}
   return set(alias)
  },
  disable:()=>set(null),
  openControlPanel(){const target=controlPanelTarget({page:'login-portal',tab:'applications'});options.launch(target.appId,{...target.params})},
 }
 if(options.entry){
  access.register=()=>change('portal.register')
  access.unregister=()=>change('portal.unregister')
 }
 access.mountPanel=element=>mountPortalPanel(access,element)
 // The window reads the status when it opens, so its settings page shows the
 // section as it is.
 void status()
 return access
}

type Node_=HTMLElement
function node(tag:string,attrs:Record<string,string>,children:Node_[]=[]):Node_{
 const out=document.createElement(tag)
 for(const [key,value] of Object.entries(attrs)){if(key==='text')out.textContent=value;else out.setAttribute(key,value)}
 out.append(...children)
 return out
}
function button(text:string,onClick:()=>void):Node_{
 const out=node('button',{type:'button',text})
 out.addEventListener('click',event=>{event.preventDefault();onClick()})
 return out
}
/** The plain panel the CLI's DSM window gives UIs without their own
 * (portal-window.ts), for the same access on an entry page. Vue apps use
 * portal/PortalSettings.vue. */
export function mountPortalPanel(access:PortalAccess,element:HTMLElement):{refresh():Promise<void>;dispose():void}{
 const messages=access.messages
 let disposed=false
 function render(current:PortalStatus,error?:unknown){
  if(disposed)return
  const parts:Node_[]=[node('h2',{text:messages.title??''})]
  const settle=(next:Promise<PortalStatus>|undefined)=>{void next?.then(fresh=>render(fresh),failure=>render(current,failure))}
  if(current.state==='active'&&current.url)parts.push(node('p',{},[node('span',{text:`${messages.link} `}),node('a',{href:current.url,target:'_blank',rel:'noopener',text:current.url})]))
  if(current.canManage&&current.entry&&current.entry!=='registered'){
   const off=current.entry==='unregistered'
   parts.push(node('p',{text:(off?messages.entryOff:messages.cleanupRequired)??''}))
   parts.push(off?button(messages.register??'',()=>settle(access.register?.())):button(messages.unregister??'',()=>settle(access.unregister?.())))
  }else if(current.canManage){
   if(current.state!=='active')parts.push(node('p',{text:messages.inactive??''}))
   const input=node('input',{type:'text',maxlength:'64',autocomplete:'off','aria-label':messages.alias??''}) as HTMLInputElement
   input.value=current.alias||current.defaultAlias
   const form=node('form',{class:'dsmapp-portal-form'},[node('label',{text:messages.alias??''}),input,node('button',{type:'submit',text:messages.apply??''})])
   form.addEventListener('submit',event=>{event.preventDefault();settle(access.apply(input.value))})
   if(current.state==='active')form.append(button(messages.disable??'',()=>settle(access.disable())))
   parts.push(form,button(messages.advanced??'',()=>access.openControlPanel()))
   if(current.entry)parts.push(button(messages.unregister??'',()=>settle(access.unregister?.())))
  }else if(current.controlPanel){
   parts.push(button(messages.controlPanel??'',()=>access.openControlPanel()))
  }else if(current.state!=='active'){
   parts.push(node('p',{text:messages.askAdmin??''}))
  }
  if(error!==undefined){
   const code=(error as {code?:unknown}|null)?.code
   parts.push(node('p',{role:'alert',class:'dsmapp-portal-error',text:(typeof code==='string'&&messages[code])||messages.portal_unavailable||''}))
  }
  element.replaceChildren(...parts)
 }
 void access.status().then(current=>render(current),()=>{})
 return {
  refresh:()=>access.status().then(current=>render(current)),
  dispose(){disposed=true;element.replaceChildren()},
 }
}
