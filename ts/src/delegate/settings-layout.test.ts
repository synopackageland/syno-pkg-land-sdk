// @vitest-environment jsdom
// (F6): Download Hub's settings page moved four times in 2 s as its
// SDK sections arrived (CLS 0.155–0.199). The folder row keeps its place
// while the status and scope permission arrive.
import {afterEach,beforeEach,expect,test} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import BackgroundSaveSettings from './BackgroundSaveSettings.vue'
import PortalSettings from '../portal/PortalSettings.vue'
import type {BackgroundSaveFacade} from './long-running'
import type {PortalAccess,PortalStatus} from '../context'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
const selection={scope:'shared:fgwmrf',root:'/fgwmrf',generation:1}
const grant={id:'g',key:'save',path:'/fgwmrf/Downloads',username:'a',lifetime:'until-revoked' as const}
const provider={async shares(){return {entries:[],total:0,offset:0}},async list(){return {entries:[],total:0,offset:0}},async info(path:string){return {name:'x',path,isDir:true}},async createFolder(){},dispose(){}}
beforeEach(()=>sessionStorage.clear())
const cleanup:Array<()=>void>=[]
afterEach(()=>cleanup.splice(0).forEach(fn=>fn()))

function saves(admin:boolean){
 const gates:Array<()=>void>=[]
 const held=<T,>(value:T)=>new Promise<T>(r=>gates.push(()=>r(value)))
 const sdk={
  status:()=>held({disabled:false,scopeMode:'install-selected',selection}),
  destination:async(key:string)=>({key,status:'active',selection,destination:grant}),
  operations:async()=>[],
 } as unknown as BackgroundSaveFacade
 const root=document.createElement('div');document.body.append(root)
 const call=async<T,>(method:string)=>(method==='delegate.status'?{scope_mode:'install-selected',selection}:{shares:[],total:0,offset:0}) as T
 const app=createApp({render:()=>h(AppRoot,{},()=>h(BackgroundSaveSettings,{sdk,admin,locale:'en',picker:{provider,keys:['save']},ctx:{call}}))})
 app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 return {root,open:async()=>{gates.splice(0).forEach(g=>g());await settle()}}
}
const rows=(root:Element)=>root.querySelectorAll('[data-save-row]').length
const placeholders=(root:Element)=>root.querySelectorAll('[data-placeholder-row]').length

for(const admin of [true,false])test(`one folder row keeps its place (${admin?'administrator':'user'})`,async()=>{
 const p=saves(admin);await settle()
 expect(placeholders(p.root)).toBe(1)
 expect(p.root.querySelector('[data-saves-placeholder],[data-save-danger]')).toBeNull()
 await p.open()
 expect(placeholders(p.root)).toBe(0)
 expect(rows(p.root)).toBe(1)
 expect(p.root.querySelector('[data-saves-list],[data-save-operations],[data-save-danger]')).toBeNull()
})

test('the Application Portal section keeps the rows an administrator will see',async()=>{
 let answer:(s:PortalStatus)=>void=()=>{}
 const portal={messages:new Proxy({},{get:(_,k)=>String(k)}),status:()=>new Promise<PortalStatus>(r=>{answer=r}),apply:async()=>active,disable:async()=>active,openControlPanel(){}} as unknown as PortalAccess
 const active:PortalStatus={state:'active',canManage:true,controlPanel:true,defaultAlias:'demo',alias:'demo',url:'https://nas/demo/'}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal}))})
 app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 await settle()
 const before=placeholders(root)
 expect(root.querySelector('[aria-busy=true]')).not.toBeNull()
 answer(active);await settle()
 expect(placeholders(root)).toBe(0)
 expect(root.querySelectorAll('[data-settings-label]').length).toBe(before)
})
