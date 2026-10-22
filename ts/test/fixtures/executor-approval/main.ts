import {createApp,h} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import './host-simulation.css'
import '@synopackageland/ui/themes/dsm7-embedded.css'
import ExecutorApproval from '../../../src/delegate/ExecutorApproval.vue'
import ExecutorNotice from '../../../src/delegate/ExecutorNotice.vue'
import BackgroundSaveSettings from '../../../src/delegate/BackgroundSaveSettings.vue'
import {ExecutorSetupKey} from '../../../src/delegate/use-executor-setup'
import type {BackgroundSaveFacade} from '../../../src/delegate/long-running'
import type {CapabilityStatus} from '../../../src/delegate/executor-setup'
// view: approval (ExecutorApproval in place of the App's pages), notice
// (ExecutorNotice and its dialog) or save (BackgroundSaveSettings).
const query=new URLSearchParams(location.search)
const admin=query.get('admin')!=='false',locale=query.get('locale')??'en',view=query.get('view')??'approval'
document.documentElement.lang=locale
let current:CapabilityStatus={state:'needs-admin-approval',...(query.has('upgrade')?{code:'task_outdated',approval:{package:'Example',old_policy_digest:'a'.repeat(64),new_policy_digest:'b'.repeat(64),new_executor_digest:'c'.repeat(64),permissions:'unchanged' as const,task_template_changed:true,root_programs:[{name:'cli',old:'f740e7379688a7d2',new:'7c18ad7f813f9164'},{name:'lifecycle',old:'f740e7379688a7d2',new:'7c18ad7f813f9164'}],policy_changes:[]}}:{})}
const files=view==='save'
const call=async<T,>(method:string)=>(method==='delegate.status'?files?{scope_mode:'install-selected',preselection:'shared:Downloads',selection:null}:{}:{shares:[{name:'Downloads',path:'/Downloads',isdir:true},{name:'Team',path:'/Team',isdir:true}],total:2,offset:0}) as T
const setup={
 async status(){return current},
 async authorize(password:string){await new Promise(r=>setTimeout(r,300));if(password!=='fixture-password')return {state:'blocked',code:'password_rejected',retry:true} as CapabilityStatus;current={state:'ready'};return current},
 async control(){return current},
 call,
}
const sdk={
 async status(){throw {code:'EXECUTOR_UNAVAILABLE'}},
 async destination(key:string){return {key,status:'missing',selection:null,destination:null}},
 async selectDestination(){throw {code:'commit-uncertain'}},
 async revoke(){return {revoked:true}},
 async operations(){return []},
} as unknown as BackgroundSaveFacade
const page=()=>h('p',{style:{margin:'0'}},'App content')
const content=()=>view==='notice'?[h(ExecutorNotice,{locale,ctx:{executor:setup,user:{isAdmin:admin} as never,reopen:()=>()=>{}}}),page()]
 :view==='save'?h(BackgroundSaveSettings,{sdk,admin,locale,picker:{provider:{dispose(){}} as never,keys:['save']}})
 :h(ExecutorApproval,{admin,locale,ctx:{reopen:()=>()=>{}}},page)
createApp({render:()=>h(AppRoot,{style:{minHeight:'100vh'}},()=>h('main',{style:view==='approval'?{}:{padding:'24px'}},content()))})
 .provide(ExecutorSetupKey,setup).mount('#app')
document.body.style.margin='0'
