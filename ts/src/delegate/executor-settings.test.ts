// @vitest-environment jsdom
import {expect,test} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import ExecutorSettings from './ExecutorSettings.vue'
import type {CapabilityStatus} from './executor-setup'
import {ExecutorSetupKey} from './use-executor-setup'
import {delegateCopy} from './copy'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
// a wrong password left only "Check status"; a retryable result keeps the form.
test('a retryable blocked approval keeps the password form for another try',async()=>{
 const blocked:CapabilityStatus&{retry:boolean}={state:'blocked',reason:'dsm_3121: DSM rejected the administrator password. Nothing was created.',retry:true}
 const setup={async status():Promise<CapabilityStatus>{return {state:'needs-admin-approval'}},async authorize(){return blocked},async control(){return blocked}}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale:'en'}))})
 app.provide(ExecutorSetupKey,setup)
 try{
  app.mount(root);await settle()
  const input=root.querySelector<HTMLInputElement>('#delegate-password')!
  input.value='wrong';input.dispatchEvent(new Event('input'));await settle()
  root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(root.textContent).toContain(delegateCopy('en').passwordRejected)
  expect(root.textContent).not.toContain('dsm_3121')
  expect(root.querySelector('#delegate-password')).not.toBeNull()
 }finally{app.unmount();root.remove()}
})
// before the first status arrives the panel is loading, not blocked.
test('the panel shows loading until the first status arrives',async()=>{
 let answer:(s:CapabilityStatus)=>void=()=>{}
 const setup={status:()=>new Promise<CapabilityStatus>(r=>{answer=r}),async authorize():Promise<CapabilityStatus>{return {state:'ready'}},async control():Promise<CapabilityStatus>{return {state:'ready'}}}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale:'en'}))})
 app.provide(ExecutorSetupKey,setup)
 try{
  app.mount(root);await settle()
  expect(root.querySelector('[aria-busy=true] [data-placeholder-row]')).not.toBeNull()
  expect(root.querySelector('[data-executor-status]')).toBeNull()
  answer({state:'ready'});await settle()
  expect(root.querySelector('[data-placeholder-row]')).toBeNull()
  expect(root.querySelector('[data-executor-status="ready"]')).not.toBeNull()
 }finally{app.unmount();root.remove()}
})
// state badge, vertical form, one action row. the regression test: an approval to
// give explains itself instead of a badge, and its button sits at the field.
async function settings(status:CapabilityStatus,ctx?:{call<T>(method:string):Promise<T>}){
 let calls=0
 const setup={async status(){calls++;return status},async authorize(){return status},async control(){return status}}
 const root=document.createElement('div');document.body.append(root)
 let exposed:{refresh():Promise<void>}|undefined
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale:'zh-TW',ctx,ref:(r:unknown)=>{exposed=r as typeof exposed}}))})
 app.provide(ExecutorSetupKey,setup)
 app.mount(root);await settle()
 return {root,calls:()=>calls,refresh:()=>exposed!.refresh(),done:()=>{app.unmount();root.remove()}}
}
test('the approval state is a badge',async()=>{
 const copy=delegateCopy('zh-TW')
 const ready=await settings({state:'ready'})
 try{expect(ready.root.querySelector('[data-executor-status="ready"]')!.textContent).toBe(copy.statusReady)}finally{ready.done()}
 const needs=await settings({state:'needs-admin-approval'})
 try{
  expect(needs.root.querySelector('[data-executor-status]')).toBeNull()
  expect(needs.root.querySelector('[data-consent-title]')!.textContent).toBe(copy.consentTitle)
 }finally{needs.done()}
})
test('fields stack above their controls and the approve button sits beside the password',async()=>{
 const p=await settings({state:'needs-admin-approval'})
 try{
  expect(p.root.querySelector('form')!.querySelector('[data-direction="vertical"],.flex-col')).not.toBeNull()
  const submit=p.root.querySelector<HTMLButtonElement>('button[type=submit]')!
  expect(submit.disabled).toBe(false)
  expect(submit.closest('[data-form-item]')!.querySelector('#delegate-password')).not.toBeNull()
  expect(Array.from(p.root.querySelectorAll('button')).map(b=>b.textContent!.trim())).not.toContain('重新整理')
 }finally{p.done()}
})
test('the page header refreshes the panel',async()=>{
 const p=await settings({state:'ready'})
 try{const before=p.calls();await p.refresh();await settle();expect(p.calls()).toBe(before+1)}finally{p.done()}
})
test('a failed check offers a retry inside its message',async()=>{
 const copy=delegateCopy('zh-TW')
 const setup={async status():Promise<CapabilityStatus>{throw new Error('down')},async authorize():Promise<CapabilityStatus>{return {state:'ready'}},async control():Promise<CapabilityStatus>{return {state:'ready'}}}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale:'zh-TW'}))})
 app.provide(ExecutorSetupKey,setup)
 try{app.mount(root);await settle();expect(root.querySelector('[role=alert]')!.querySelector('button')!.textContent!.trim()).toBe(copy.retry)}
 finally{app.unmount();root.remove()}
})
// the confirmed scope is a PathDisplay.
test('the confirmed scope shows as PathDisplay',async()=>{
 const ctx={async call<T>(method:string){return (method==='delegate.status'?{scope_mode:'fixed',selection:{scope:'shared:media',root:'/media',generation:1}}:{shares:[],total:0,offset:0}) as T}}
 const p=await settings({state:'ready'},ctx)
 try{expect(p.root.querySelector('.ui-path-display [data-ui-path-text][title="/media"]')).not.toBeNull()}finally{p.done()}
})
// a root-ops App shows this panel too; only the executor's reported
// file scope makes it speak of files.
async function english(status:CapabilityStatus,ctx?:{call<T>(method:string):Promise<T>}){
 const setup={async status(){return status},async authorize(){return status},async control(){return status}}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale:'en',ctx}))})
 app.provide(ExecutorSetupKey,setup)
 app.mount(root);await settle()
 return {root,done:()=>{app.unmount();root.remove()}}
}
test('without a file scope the approval does not speak of files',async()=>{
 const copy=delegateCopy('en')
 for(const status of [{state:'needs-admin-approval'},{state:'blocked',reason:'x'}] as CapabilityStatus[]){
  const p=await english(status)
  try{
   expect(p.root.textContent).not.toMatch(/file/i)
   if(status.state==='blocked'){expect(p.root.textContent).toContain(copy.setupHelp);expect(p.root.textContent).toContain(copy.executorBlocked)}
   else expect(p.root.querySelector('[data-consent-lead]')!.textContent).toContain(copy.consentPurpose)
  }finally{p.done()}
 }
 const p=await english({state:'needs-admin-approval'})
 try{expect(p.root.querySelector('button[type=submit]')!.textContent).toBe(copy.authorize)}finally{p.done()}
})
test('an executor that reports a file scope explains file access',async()=>{
 const copy=delegateCopy('en')
 const ctx={async call<T>(){return {scope_mode:'fixed'} as T}}
 const p=await english({state:'needs-admin-approval'},ctx)
 try{
  expect(p.root.querySelector('[data-consent-lead]')!.textContent).toContain(copy.consentPurposeFiles)
 }finally{p.done()}
})
// recheck: approving took 45 s of a bare "Confirming"; the panel says why it waits.
test('a confirming approval says it can take a minute',async()=>{
 const copy=delegateCopy('en')
 const setup={async status():Promise<CapabilityStatus>{return {state:'needs-admin-approval'}},authorize:()=>new Promise<CapabilityStatus>(()=>{}),async control():Promise<CapabilityStatus>{return {state:'ready'}}}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale:'en'}))})
 app.provide(ExecutorSetupKey,setup)
 try{
  app.mount(root);await settle()
  expect(root.textContent).not.toContain(copy.confirmingHelp)
  const input=root.querySelector<HTMLInputElement>('#delegate-password')!
  input.value='secret';input.dispatchEvent(new Event('input'));await settle()
  root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(root.textContent).toContain(copy.confirmingHelp)
  // the confirm button looked pressable while the approval ran.
  const submit=root.querySelector<HTMLButtonElement>('button[type=submit]')!
  expect(submit.disabled).toBe(true)
  expect(submit.getAttribute('data-loading')).not.toBeNull()
 }finally{app.unmount();root.remove()}
})
// changing an approved scope belongs to BackgroundSaveSettings; this
// panel asks for the password only while something needs approving.
test('an approved executor with its scope asks for no password',async()=>{
 const ctx={async call<T>(method:string){return (method==='delegate.status'?{scope_mode:'install-selected',selection:{scope:'shared:media',root:'/media',generation:1}}:{shares:[{name:'media',path:'/media',isdir:true}],total:1,offset:0}) as T}}
 const p=await settings({state:'ready'},ctx)
 try{expect(p.root.querySelector('input[type=password]')).toBeNull()}finally{p.done()}
})
