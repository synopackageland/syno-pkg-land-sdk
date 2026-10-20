// @vitest-environment jsdom
// after a wrong password the window showed "Unavailable" with
// DSM's English reason and a hint to check the App setup, and the next status
// read, about 3 s later, put "Needs approval" back with no word of the error.
import {expect,test} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import AppShell from '../shell/AppShell.vue'
import ExecutorApproval from './ExecutorApproval.vue'
import ExecutorSettings from './ExecutorSettings.vue'
import type {CapabilityStatus,ExecutorSetup} from './executor-setup'
import {ExecutorSetupKey} from './use-executor-setup'
import {delegateCopy,delegateCatalogs} from './copy'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
const rejected:CapabilityStatus={state:'blocked',reason:'dsm_3121: DSM rejected the administrator password. Nothing was created.',retry:true}
const needs:CapabilityStatus={state:'needs-admin-approval',reason:'Task is missing.'}

// As the DSM window's access does: a refused approval is published, and
// status reads it back as still needing approval.
function windowAccess(answer:CapabilityStatus=rejected){
 const listeners:Array<(s:CapabilityStatus)=>void>=[]
 const publish=(s:CapabilityStatus)=>{listeners.forEach(l=>l(s));return s}
 const setup:ExecutorSetup={
  status:async()=>publish(needs),
  authorize:async()=>publish(answer),
  async control(){return needs},
  subscribe(listener){listeners.push(listener)},
 }
 return {setup,poll:()=>setup.status()}
}

async function submit(scope:ParentNode,password:string){
 const input=scope.querySelector<HTMLInputElement>('#delegate-password')!
 input.value=password;input.dispatchEvent(new Event('input'));await settle()
 scope.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
}

function mountIn(render:()=>ReturnType<typeof h>,setup:ExecutorSetup){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render})
 app.provide(ExecutorSetupKey,setup)
 app.mount(root)
 return {root,done:()=>{app.unmount();root.remove()}}
}

test('a wrong password says so in every SDK language, without DSM\'s reason or the setup hint',async()=>{
 for(const locale of Object.keys(delegateCatalogs)){
  const copy=delegateCopy(locale),access=windowAccess()
  const p=mountIn(()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale})),access.setup)
  try{
   await settle();await submit(p.root,'wrong')
   const alert=p.root.querySelector('[role=alert]')
   expect(alert?.textContent,locale).toContain(copy.passwordRejected)
   expect(p.root.textContent).not.toContain('dsm_3121')
   expect(p.root.textContent).not.toContain(copy.executorBlocked)
   expect(p.root.textContent).not.toContain(copy.setupError)
   expect(p.root.querySelector<HTMLInputElement>('#delegate-password')!.disabled).toBe(false)
  }finally{p.done()}
 }
 const texts=Object.values(delegateCatalogs).map(c=>c.passwordRejected)
 expect(new Set(texts).size).toBe(texts.length)
})

test('the window keeps the password error through status reads until the next submit',async()=>{
 const copy=delegateCopy('zh-TW'),access=windowAccess()
 let reopened=()=>{}
 const p=mountIn(()=>h(AppRoot,{},()=>h(ExecutorApproval,{admin:true,locale:'zh-TW',ctx:{reopen:(fn:()=>void)=>{reopened=fn;return ()=>{}}}},()=>h('p','App content'))),access.setup)
 try{
  await settle();await submit(p.root,'wrong')
  expect(p.root.querySelector('[role=alert]')?.textContent).toContain(copy.passwordRejected)
  for(let i=0;i<3;i++){await access.poll();reopened();await settle()}
  expect(p.root.querySelector('[role=alert]')?.textContent).toContain(copy.passwordRejected)
  expect(p.root.querySelector('[data-executor-consent]')).not.toBeNull()
  access.setup.authorize=()=>new Promise<CapabilityStatus>(()=>{})
  await submit(p.root,'again')
  expect(p.root.querySelector('[role=alert]')).toBeNull()
 }finally{p.done()}
})

test('another failed approval keeps its reason through status reads',async()=>{
 const copy=delegateCopy('en'),access=windowAccess({state:'blocked',reason:'executor_status_mismatch: the approved executor is not answering.',retry:true})
 const p=mountIn(()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale:'en'})),access.setup)
 try{
  await settle();await submit(p.root,'secret')
  await access.poll();await settle()
  const alert=p.root.querySelector('[role=alert]')
  expect(alert?.textContent).toContain(copy.setupError)
  expect(alert?.textContent).toContain('executor_status_mismatch')
  expect(p.root.querySelector<HTMLInputElement>('#delegate-password')!.disabled).toBe(false)
 }finally{p.done()}
})

test('the notice dialog keeps the password error through status reads until it is closed',async()=>{
 const copy=delegateCopy('en'),access=windowAccess()
 const ctx={fullPage:false,locale:'en',executor:access.setup,user:{name:'admin',isAdmin:true},reopen:()=>()=>{},call:async<T,>()=>({}) as T}
 const p=mountIn(()=>h(AppShell,{appRoot:true,ctx,locale:'en',modelValue:'home',pages:[{id:'home',title:'Home'}]},()=>h('p','App content')),access.setup)
 const dialog=()=>document.querySelector('[role=dialog]')
 const approve=()=>Array.from(document.querySelectorAll('button')).find(b=>b.textContent?.trim()===copy.noticeApprove)!
 try{
  await settle();approve().click();await settle()
  await submit(dialog()!,'wrong')
  await access.poll();await settle()
  expect(dialog()?.querySelector('[role=alert]')?.textContent).toContain(copy.passwordRejected)
  // The dialog's own close button, first in its header.
  dialog()!.querySelector('button')!.click();await settle()
  expect(dialog()).toBeNull()
  approve().click();await settle()
  expect(dialog()!.querySelector('[role=alert]')).toBeNull()
 }finally{p.done()}
})

// The DSM window's access reports a refusal in code; reason stays its English detail.
test('a PasswordConfirm refusal reported in code shows its own text in every SDK language, never the English reason',async()=>{
 const cases:Array<[string,keyof ReturnType<typeof delegateCopy>]>=[['password_rejected','passwordRejected'],['password_required','passwordRequired'],['password_confirm_failed','passwordConfirmFailed']]
 for(const [code,key] of cases){
  for(const locale of Object.keys(delegateCatalogs)){
   const copy=delegateCopy(locale),access=windowAccess({state:'blocked',code,reason:'dsm_4100: DSM said something in English.',retry:true})
   const p=mountIn(()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale})),access.setup)
   try{
    await settle();await submit(p.root,'pw')
    await access.poll();await settle()
    expect(p.root.querySelector('[role=alert]')?.textContent,`${code} ${locale}`).toContain(copy[key])
    expect(p.root.textContent).not.toContain('said something')
    expect(p.root.textContent).not.toContain(copy.setupError)
   }finally{p.done()}
  }
 }
 for(const key of ['passwordRequired','passwordConfirmFailed'] as const){
  const texts=Object.values(delegateCatalogs).map(c=>c[key])
  expect(new Set(texts).size,key).toBe(texts.length)
 }
})

test('an older CLI\'s refused password confirmation still reads as one',async()=>{
 const copy=delegateCopy('en'),access=windowAccess({state:'blocked',reason:'dsm_119: administrator password confirmation failed. Nothing was created.',retry:true})
 const p=mountIn(()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale:'en'})),access.setup)
 try{
  await settle();await submit(p.root,'pw')
  expect(p.root.querySelector('[role=alert]')?.textContent).toContain(copy.passwordConfirmFailed)
 }finally{p.done()}
})

test('an ended DSM session asks to sign in again instead of checking the App setup',async()=>{
 const copy=delegateCopy('zh-TW'),access=windowAccess({state:'blocked',code:'session_ended',reason:'dsm_119: the DSM session ended. Sign in to DSM again, then approve again.',retry:true})
 const p=mountIn(()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale:'zh-TW'})),access.setup)
 try{
  await settle();await submit(p.root,'pw')
  const alert=p.root.querySelector('[role=alert]')
  expect(alert?.textContent).toContain(copy.session)
  expect(alert?.textContent).not.toContain(copy.setupError)
 }finally{p.done()}
})

test('the window approval keeps its state through a refusal reported in code',async()=>{
 const access=windowAccess({state:'blocked',code:'password_rejected',reason:'a reason the SDK does not parse',retry:true})
 const p=mountIn(()=>h(AppRoot,{},()=>h(ExecutorApproval,{admin:true,locale:'en',ctx:{reopen:()=>()=>{}}},()=>h('p','App content'))),access.setup)
 try{
  await settle();await submit(p.root,'wrong')
  expect(p.root.querySelector('[data-executor-consent]')).not.toBeNull()
 }finally{p.done()}
})
