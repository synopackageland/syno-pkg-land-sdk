// @vitest-environment jsdom
import {expect,test} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import ExecutorSettings from './ExecutorSettings.vue'
import type {CapabilityStatus} from './executor-setup'
import {ExecutorSetupKey} from './use-executor-setup'
import {delegateCopy} from './copy'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
const selection={scope:'shared:reports',root:'/reports',generation:1}
async function approve(answers:CapabilityStatus[],selectedAfter:number){
 const calls:Array<[string,string|undefined]>=[]
 let state:CapabilityStatus={state:'needs-admin-approval'}
 const setup={async status(){return state},async authorize(password:string,scope?:string){calls.push([password,scope]);state=answers[Math.min(calls.length,answers.length)-1];return state},async control(){return state}}
 const ctx={async call<T>(method:string):Promise<T>{
  if(method==='bg.browse')return {shares:[{name:'reports',path:'/reports',isdir:true}],total:1,offset:0} as T
  return {scope_mode:'install-selected',preselection:'shared:reports',selection:calls.length>=selectedAfter?selection:null} as T
 }}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorSettings,{admin:true,locale:'en',ctx}))})
 app.provide(ExecutorSetupKey,setup)
 app.mount(root);await settle()
 const input=root.querySelector<HTMLInputElement>('#delegate-password')!
 input.value='secret';input.dispatchEvent(new Event('input'));await settle()
 root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
 return {calls,text:root.textContent!,done:()=>{app.unmount();root.remove()}}
}
// after reinstall the approval left the share unconfirmed and
// the administrator had to enter the password a second time.
test('an approval that leaves the share unconfirmed confirms it in the same submit',async()=>{
 const run=await approve([{state:'ready'},{state:'ready'}],2)
 try{
  expect(run.calls).toEqual([['secret','shared:reports'],['secret','shared:reports']])
  expect(run.text).toContain(delegateCopy('en').statusReady)
  expect(run.text).not.toContain(delegateCopy('en').consentTitle)
 }finally{run.done()}
})
// A refused password is never sent again: DSM may lock the account.
test('a refused approval is not repeated',async()=>{
 const run=await approve([{state:'blocked',reason:'dsm_3121',retry:true}],2)
 try{expect(run.calls).toHaveLength(1)}finally{run.done()}
})
// One approval that confirmed the share needs nothing more.
test('an approval that confirmed the share is sent once',async()=>{
 const run=await approve([{state:'ready'}],1)
 try{expect(run.calls).toHaveLength(1)}finally{run.done()}
})
