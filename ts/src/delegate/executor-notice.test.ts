// @vitest-environment jsdom
import {scopeShares} from '../../test/dsm-files-fixture'
import {expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import AppShell from '../shell/AppShell.vue'
import type {CapabilityStatus,ExecutorSetup} from './executor-setup'
import {delegateCopy} from './copy'
import {ExecutorSetupKey} from './use-executor-setup'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
const copy=delegateCopy('en')

function fakeSetup(first:CapabilityStatus){
 const passwords:string[]=[],listeners:Array<(s:CapabilityStatus)=>void>=[]
 let current=first
 const setup:ExecutorSetup={
  status:async()=>current,
  async authorize(password){passwords.push(password);current={state:'ready'};listeners.forEach(l=>l(current));return current},
  async control(){return current},
  subscribe(listener){listeners.push(listener)},
 }
 return {setup,passwords,publish(s:CapabilityStatus){current=s;listeners.forEach(l=>l(s))}}
}

// recheck: after an upgrade that changed the policy, a delegate App
// showed nothing while its calls failed with EXECUTOR_UNAVAILABLE.
async function mount(executor:ExecutorSetup|undefined,isAdmin=true,reopen:(fn:()=>void)=>()=>void=()=>()=>{},fakeTimers=false){
 const ctx={fullPage:false,locale:'en',executor,user:{name:'someone',isAdmin},reopen,call:async<T,>()=>({}) as T}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{appRoot:true,ctx,locale:'en',modelValue:'home',pages:[{id:'home',title:'Home'}]},()=>h('p',{class:'content'},'App content'))})
 app.mount(root)
 if(fakeTimers)await vi.advanceTimersByTimeAsync(0);else await settle()
 const button=()=>Array.from(document.querySelectorAll('button')).find(b=>b.textContent?.trim()===copy.noticeApprove)
 return {root,button,content:()=>root.querySelector('.content'),done:()=>{app.unmount();root.remove()}}
}

test('an administrator sees a notice over the content and approves in a dialog',async()=>{
 const {setup,passwords}=fakeSetup({state:'needs-admin-approval'})
 const shell=await mount(setup)
 try{
  expect(shell.content()).not.toBeNull()
  expect(shell.root.textContent).toContain(copy.noticeAdmin)
  expect(document.querySelector('#delegate-password')).toBeNull()
  shell.button()!.click();await settle()
  // The dialog names what is approved; the settings inside drop their section title.
  expect(document.querySelector('[role=dialog] h2,[role=dialog] [data-dialog-title]')?.textContent?.trim()).toBe(copy.setupTitle)
  const input=document.querySelector<HTMLInputElement>('[role=dialog] #delegate-password')!
  input.value='secret';input.dispatchEvent(new Event('input'));await settle()
  document.querySelector('[role=dialog] form')!.dispatchEvent(new Event('submit'));await settle()
  expect(passwords).toEqual(['secret'])
  expect(shell.root.textContent).not.toContain(copy.noticeAdmin)
  expect(document.querySelector('[role=dialog]')).toBeNull()
  expect(shell.content()).not.toBeNull()
 }finally{shell.done()}
})

test('anyone else is told to ask an administrator, with no way to approve',async()=>{
 const shell=await mount(fakeSetup({state:'blocked',reason:'x'}).setup,false)
 try{
  expect(shell.content()).not.toBeNull()
  expect(shell.root.textContent).toContain(copy.noticeUser)
  expect(shell.button()).toBeUndefined()
 }finally{shell.done()}
})

test('an approved executor shows no notice until a later status is not ready',async()=>{
 const {setup,publish}=fakeSetup({state:'ready'})
 const shell=await mount(setup)
 try{
  expect(shell.root.textContent).not.toContain(copy.noticeAdmin)
  publish({state:'needs-admin-approval'});await settle()
  expect(shell.root.textContent).toContain(copy.noticeAdmin)
 }finally{shell.done()}
})

test('reopening the window checks the executor again',async()=>{
 const {setup}=fakeSetup({state:'ready'})
 let reopened=()=>{}
 const shell=await mount(setup,true,fn=>{reopened=fn;return ()=>{}})
 try{
  setup.status=async()=>({state:'needs-admin-approval'})
  reopened();await settle()
  expect(shell.root.textContent).toContain(copy.noticeAdmin)
 }finally{shell.done()}
})

test('an App without an executor shows no notice',async()=>{
 const shell=await mount(undefined)
 try{expect(shell.root.textContent).not.toContain(copy.noticeAdmin);expect(shell.button()).toBeUndefined()}finally{shell.done()}
})

// the dialog read the scope with the App's raw call, whose
// bg.browse Download Hub does not serve; the settings page used the App's
// adapter. The dialog uses the setup the App provides, with its call.
test('the dialog reads the scope through the call of the setup the App provides',async()=>{
 const {setup}=fakeSetup({state:'needs-admin-approval'})
 const raw:string[]=[],adapted:string[]=[]
 const call=async<T,>(method:string):Promise<T>=>{
  raw.push(method)
  if(method==='delegate.status')return {scope_mode:'install-selected'} as T
  throw {code:'unknown_method'}
 }
 scopeShares([{name:'media',path:'/media',isdir:true}])
 const provided:ExecutorSetup={...setup,call:async<T,>(method:string)=>{adapted.push(method);return (method==='bg.browse'?{shares:[{name:'media',path:'/media',isdir:true}],total:1,offset:0}:await call<T>(method)) as T}}
 const ctx={fullPage:false,locale:'en',executor:setup,user:{name:'someone',isAdmin:true},reopen:()=>()=>{},call}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{appRoot:true,ctx,locale:'en',modelValue:'home',pages:[{id:'home',title:'Home'}]},()=>h('p','App content'))})
 app.provide(ExecutorSetupKey,provided)
 try{
  app.mount(root);await settle()
  Array.from(document.querySelectorAll('button')).find(b=>b.textContent?.trim()===copy.noticeApprove)!.click();await settle()
  expect(adapted).toContain('delegate.status');expect(adapted).not.toContain('bg.browse')
  expect(raw).not.toContain('bg.browse')
  expect(document.querySelector('[role=dialog] [role=alert]')).toBeNull()
  expect(document.querySelector<HTMLInputElement>('[role=dialog] #delegate-password')!.disabled).toBe(false)
 }finally{app.unmount();root.remove()}
})

// an approved executor's status failed in the browser for a user
// who is not an administrator, and the notice said it waited for approval.
test('a status that cannot be read shows no notice to anyone else',async()=>{
 const failing:ExecutorSetup={status:async()=>{throw {code:'admin_required'}},async authorize(){return {state:'ready'}},async control(){return {state:'ready'}}}
 const shell=await mount(failing,false)
 try{expect(shell.root.textContent).not.toContain(copy.noticeUser);expect(shell.root.querySelector('.executor-notice')).toBeNull()}finally{shell.done()}
})

// a status that cannot be read asked an administrator to approve
// an executor that may be approved; the notice stays away and checks again.
test('a status that cannot be read asks no administrator to approve, and is checked again',async()=>{
 vi.useFakeTimers()
 let reads=0
 const failing:ExecutorSetup={status:async()=>{reads++;throw {code:'EXECUTOR_PROTOCOL'}},async authorize(){return {state:'ready'}},async control(){return {state:'ready'}}}
 const shell=await mount(failing,true,undefined,true)
 try{
  expect(shell.root.textContent).not.toContain(copy.noticeAdmin)
  expect(shell.button()).toBeUndefined()
  await vi.advanceTimersByTimeAsync(2000)
  expect(reads).toBe(2)
 }finally{shell.done();vi.useRealTimers()}
})

// while Download Hub was stopped its notice asked for approval,
// and after the start it stayed until the page was reloaded.
test('a stopped package says so, asks for no approval and comes back by itself',async()=>{
 vi.useFakeTimers()
 const {setup}=fakeSetup({state:'blocked',code:'executor_unreachable',reason:'APP_STOPPED: the executor status request failed.',retry:true})
 const shell=await mount(setup,true,undefined,true)
 try{
  expect(shell.root.textContent).toContain(copy.appStopped)
  expect(shell.root.textContent).not.toContain(copy.noticeAdmin)
  expect(shell.button()).toBeUndefined()
  setup.status=async()=>({state:'blocked',code:'executor_starting',reason:"executor_starting: the executor's start step has not ended."})
  await vi.advanceTimersByTimeAsync(2000)
  expect(shell.root.textContent).toContain(copy.appStarting)
  setup.status=async()=>({state:'ready'})
  await vi.advanceTimersByTimeAsync(4000)
  expect(shell.root.querySelector('.executor-notice')).toBeNull()
 }finally{shell.done();vi.useRealTimers()}
})

test('the checks of a stopped package back off and end with the page',async()=>{
 vi.useFakeTimers()
 let reads=0
 const stopped:ExecutorSetup={status:async()=>{reads++;return {state:'blocked',reason:'APP_STOPPED'}},async authorize(){return {state:'ready'}},async control(){return {state:'ready'}}}
 const shell=await mount(stopped,false,undefined,true)
 await vi.advanceTimersByTimeAsync(60000)
 // 2, 4, 8, 15, 15, 15 s: six checks after the first in a minute, not thirty.
 expect(reads).toBeLessThanOrEqual(8)
 shell.done()
 const after=reads
 await vi.advanceTimersByTimeAsync(60000)
 expect(reads).toBe(after)
 vi.useRealTimers()
})
