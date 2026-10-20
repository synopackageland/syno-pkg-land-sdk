import {createApp,h} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import '@synopackageland/ui/themes/dsm7-embedded.css'
import AppShell from '../../../src/shell/AppShell.vue'
import AuthorizationGate from '../../../src/authorization/AuthorizationGate.vue'
import AuthorizationSettings from '../../../src/authorization/AuthorizationSettings.vue'
import DelegatePanel from '../../../src/delegate/DelegatePanel.vue'
import BackgroundPanel from '../../../src/background/BackgroundPanel.vue'
import ExecutorSettings from '../../../src/delegate/ExecutorSettings.vue'
import PortalSettings from '../../../src/portal/PortalSettings.vue'
import {ExecutorSetupKey} from '../../../src/delegate/use-executor-setup'
import {portalMessages} from '../../../src/portal/i18n'
import type {AuthorizationStatus,PortalStatus} from '../../../src/context'
import type {CapabilityStatus} from '../../../src/delegate/executor-setup'
// view: gate (the authorization dialog), unavailable (a check that failed),
// settings (an authorized package's settings section), delegate, background,
// executor (a status the executor did not answer) or portal; locale: en, zh-TW, ...
const query=new URLSearchParams(location.search)
const locale=query.get('locale')??'en',view=query.get('view')??'gate'
document.documentElement.lang=locale
const reopen=()=>()=>{}
const authorization=(state:AuthorizationStatus['state'],reason?:string)=>({
 canManage:()=>true,
 async status(){if(state==='unavailable')throw {code:'service_unavailable'};return {command:'authorization',schema_version:1,state,reason} as AuthorizationStatus},
 async request(){return {command:'authorization',schema_version:1,state:'ready'} as AuthorizationStatus},
 async revoke(){return {command:'authorization',schema_version:1,state:'authorization_required',reason:'grant_missing'} as AuthorizationStatus},
})
const later='2026-10-18T09:00:00Z',owner={user:'admin',uid:'1024'}
const replies:Record<string,unknown>={
 'delegate.status':{state:'ready'},
 'delegate.list':{delegations:[{delegation_id:'d1',job_id:'j1',path:'/downloads/films',expires_at:later}]},
 'delegate.jobs.list':{jobs:[{id:'j1',name:'ubuntu-24.04.iso',state:'running'},{id:'j2',name:'report.pdf',state:'succeeded'},{id:'j3',name:'archive.zip',state:'failed'},{id:'j4',name:'photos.tar',state:'waiting_executor'}]},
 'bg.status':{owner,admin:true,revision:1,worker:{username:'MyApp',alive:true},limits:{maxInputBytes:1048576,maxQueued:16,maxOwnerPending:4,maxOwnerBytes:4194304,jobTimeoutSeconds:600}},
 'bg.jobs.list':{jobs:[{id:'b1',name:'backup.tar',state:'running',submitter:owner,progress:{done:3,total:10,bytes:2097152,totalBytes:10485760}},{id:'b2',name:'notes.txt',state:'succeeded',submitter:owner,copyRetained:true}]},
 'bg.schedules.list':{schedules:[{id:'s1',name:'notes.txt',owner,enabled:true,nextAt:later,intervalSeconds:3600,workerRevision:1,missed:0}]},
}
const call=async<T,>(method:string)=>{if(!(method in replies))throw {code:'unknown_method'};return replies[method] as T}
const unreachable:CapabilityStatus={state:'blocked',reason:'delegate.status: the executor status request failed.',retry:true}
const executor={async status(){return unreachable},async authorize(){return unreachable},async control(){return unreachable},call}
const portalStatus:PortalStatus={state:'active',url:`${location.origin}/myapp/`,alias:'myapp',defaultAlias:'myapp',canManage:true} as PortalStatus
const portal={messages:portalMessages(locale),async status(){return portalStatus},lastStatus:()=>portalStatus,async apply(){return portalStatus},async disable(){return portalStatus},openControlPanel(){}}
const ctx={locale,displayName:'MyApp',call,reopen}
const titles:Record<string,string>={delegate:'Downloads',background:'Background jobs',executor:'Settings',portal:'Settings',settings:'Settings'}
function page(){
 if(view==='delegate')return h(DelegatePanel,{ctx})
 if(view==='background')return h(BackgroundPanel,{ctx})
 if(view==='executor')return h(ExecutorSettings,{admin:true,locale,initial:unreachable})
 if(view==='portal')return h(PortalSettings,{portal})
 return h(AuthorizationSettings)
}
const shell=()=>h(AppShell,{locale,modelValue:'page',pages:[{id:'page',title:titles[view]??'MyApp'}]},page)
const gate=(state:AuthorizationStatus['state'],reason?:string)=>h(AuthorizationGate,{ctx:{locale,displayName:'MyApp',reopen,authorization:authorization(state,reason)}},shell)
const content=()=>view==='gate'?gate('authorization_required','grant_missing'):view==='unavailable'?gate('unavailable'):view==='settings'?gate('ready'):shell()
const app=createApp({render:()=>h(AppRoot,{style:{height:'100vh'}},content)})
app.config.idPrefix='sdk-surfaces-fixture-'
app.provide(ExecutorSetupKey,executor)
app.mount('#app')
document.body.style.margin='0'
