import {createApp,h} from 'vue'
import {AppRoot,SettingsSection,SettingsRow} from '@synopackageland/ui'
import '@synopackageland/ui/themes/dsm7-embedded.css'
import BackgroundSaveSettings from '../../../src/delegate/BackgroundSaveSettings.vue'
import {ExecutorSetupKey} from '../../../src/delegate/use-executor-setup'
import type {BackgroundSaveFacade,DestinationGrant} from '../../../src/delegate/long-running'
const admin=new URLSearchParams(location.search).get('admin')==='true'
let selection={scope:'shared:Downloads',root:'/Downloads',generation:1}
let destination:DestinationGrant|null={id:'fixture-grant',key:'save',path:admin?'/Downloads':'/Downloads/Team',username:admin?'admin':'user',lifetime:'until-revoked'}
const pause=()=>new Promise(r=>setTimeout(r,500))
const sdk={
 async status(){await pause();return {disabled:false,scopeMode:'install-selected',selection}},
 async destination(key:string){return {key,status:destination?'active':'missing',selection,destination}},
 async selectDestination(input:{key:string;path:string}){destination={id:'fixture-grant',username:'admin',lifetime:'until-revoked',...input};return destination},
 async revoke(){destination=null;return {revoked:true}},
 async operations(){throw new Error('Settings must not read history')},
} as unknown as BackgroundSaveFacade
const call=async<T,>(method:string)=>(method==='delegate.status'?{scope_mode:'install-selected',selection}:{shares:[{name:'Downloads',path:'/Downloads',isdir:true},{name:'Team',path:'/Team',isdir:true}],total:2,offset:0}) as T
const setup={async status(){await pause();return {state:'ready' as const}},async authorize(password:string,scope?:string){if(password!=='fixture-password')return {state:'blocked' as const,code:'password_rejected',retry:true};selection={scope:scope!,root:scope==='home'?'/home':'/'+scope!.slice(7),generation:selection.generation+1};return {state:'ready' as const}},async control(){return {state:'ready' as const}},call}
const provider={async shares(){return {entries:[],offset:0,total:0}},async list(){return {entries:[],offset:0,total:0}},async info(path:string){return {name:path.slice(1),path,isDir:true}},async createFolder(){},dispose(){}}
const app=createApp({render:()=>h(AppRoot,{style:{minHeight:'100vh'}},()=>h('main',{style:{padding:'24px',maxWidth:'1100px',margin:'0 auto'}},[
 h(BackgroundSaveSettings,{sdk,admin,locale:'zh-TW',picker:{provider,keys:['save']}}),
 h(SettingsSection,{title:'其他設定','data-next-section':''},()=>h(SettingsRow,{label:'連線'},()=> '已連線')),
]))})
app.config.idPrefix='background-settings-fixture-'
app.provide(ExecutorSetupKey,setup)
app.mount('#app')
document.body.style.margin='0'
