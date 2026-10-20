// @vitest-environment jsdom
// an App opened through its Application Portal alias showed
// "bad_response: the executor status request failed." as the only detail of
// an approved executor, in English, with no word of what to do.
import {expect,test} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import ExecutorSettings from './ExecutorSettings.vue'
import type {CapabilityStatus,ExecutorSetup} from './executor-setup'
import {ExecutorSetupKey} from './use-executor-setup'
import {delegateCopy,delegateCatalogs} from './copy'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
// As the DSM window's access reports a status request that got no CGI answer.
const unreachable:CapabilityStatus={state:'blocked',reason:'bad_response: the executor status request failed.',retry:true}

function mountIn(setup:ExecutorSetup,locale:string){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale}))})
 app.provide(ExecutorSetupKey,setup)
 app.mount(root)
 return {root,done:()=>{app.unmount();root.remove()}}
}
const reading=(status:CapabilityStatus):ExecutorSetup=>({status:async()=>status,authorize:async()=>status,async control(){return status}})

test('a failed status request says what to do in every SDK language, the code kept as detail',async()=>{
 for(const locale of Object.keys(delegateCatalogs)){
  const copy=delegateCopy(locale),p=mountIn(reading(unreachable),locale)
  try{
   await settle()
   expect(p.root.textContent,locale).toContain(copy.statusUnreachable)
   expect(p.root.querySelector('.executor-reason')?.textContent,locale).toBe(unreachable.reason)
  }finally{p.done()}
 }
 const texts=Object.values(delegateCatalogs).map(c=>c.statusUnreachable)
 expect(new Set(texts).size).toBe(texts.length)
})

test('an approval whose executor then gives no status answer says so in the alert',async()=>{
 const copy=delegateCopy('zh-TW'),needs:CapabilityStatus={state:'needs-admin-approval',reason:'Task is missing.'}
 const p=mountIn({...reading(needs),authorize:async()=>unreachable},'zh-TW')
 try{
  await settle()
  const input=p.root.querySelector<HTMLInputElement>('#delegate-password')!
  input.value='secret';input.dispatchEvent(new Event('input'));await settle()
  p.root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  const alert=p.root.querySelector('[role=alert]')
  expect(alert?.textContent).toContain(copy.statusUnreachable)
  expect(alert?.textContent).toContain('bad_response')
 }finally{p.done()}
})

test('another blocked reason is shown as it is',async()=>{
 const reason='executor_status_mismatch: the running executor reports other digests than this package pins.'
 const copy=delegateCopy('en'),p=mountIn(reading({state:'blocked',reason}),'en')
 try{
  await settle()
  expect(p.root.textContent).toContain(reason)
  expect(p.root.textContent).not.toContain(copy.statusUnreachable)
 }finally{p.done()}
})

// an executor still running its package start step after the window
// stopped waiting is not broken: say so, in the DSM language. A status read
// says the package is starting, with no form, and checks again;
// an approval that ends there explains it in the alert.
test('a start step that outlasts the wait is said to be starting, and explained in the alert',async()=>{
 const starting:CapabilityStatus={state:'blocked',code:'executor_starting',reason:"executor_starting: the executor's start step has not ended.",retry:true}
 for(const locale of Object.keys(delegateCatalogs)){
  const copy=delegateCopy(locale),p=mountIn(reading(starting),locale)
  try{
   await settle()
   expect(p.root.textContent,locale).toContain(copy.appStarting)
   expect(p.root.querySelector('#delegate-password'),locale).toBeNull()
  }finally{p.done()}
 }
 const texts=Object.values(delegateCatalogs).map(c=>c.statusStarting)
 expect(new Set(texts).size).toBe(texts.length)
 const copy=delegateCopy('zh-TW'),p=mountIn({...reading({state:'needs-admin-approval'}),authorize:async()=>starting},'zh-TW')
 try{
  await settle()
  const input=p.root.querySelector<HTMLInputElement>('#delegate-password')!
  input.value='secret';input.dispatchEvent(new Event('input'));await settle()
  p.root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(p.root.querySelector('[role=alert]')?.textContent).toContain(copy.statusStarting)
 }finally{p.done()}
})
