// @vitest-environment jsdom
import {expect,test} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import ExecutorApproval from './ExecutorApproval.vue'
import type {CapabilityStatus,ExecutorSetup} from './executor-setup'
import {ExecutorSetupKey} from './use-executor-setup'
import {delegateCopy} from './copy'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}

function fakeSetup(first:CapabilityStatus|Promise<CapabilityStatus>,approved:CapabilityStatus={state:'ready'}){
 const passwords:string[]=[],listeners:Array<(s:CapabilityStatus)=>void>=[]
 let current=first
 const setup:ExecutorSetup={
  status:async()=>current,
  async authorize(password){passwords.push(password);current=approved;return approved},
  async control(){return current},
  subscribe(listener){listeners.push(listener)},
 }
 return {setup,passwords,publish(s:CapabilityStatus){current=s;listeners.forEach(l=>l(s))}}
}

async function mount(setup:ExecutorSetup|undefined,props:{admin:boolean;ctx?:{reopen(fn:()=>void):()=>void;fullPage?:boolean;displayName?:string;appIcon?:string}}={admin:true}){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorApproval,{...props,locale:'en'},()=>h('p',{class:'content'},'App content')))})
 if(setup)app.provide(ExecutorSetupKey,setup)
 app.mount(root);await settle()
 return {root,content:()=>root.querySelector('.content'),done:()=>{app.unmount();root.remove()}}
}

async function approve(root:HTMLElement,password:string){
 const input=root.querySelector<HTMLInputElement>('#delegate-password')!
 input.value=password;input.dispatchEvent(new Event('input'));await settle()
 root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
}

// the approval takes the user password in the window, not in a dialog.
test('an administrator approves with the user password in place of the content, which then shows',async()=>{
 const {setup,passwords}=fakeSetup({state:'needs-admin-approval'})
 const shell=await mount(setup)
 try{
  expect(shell.content()).toBeNull()
  expect(shell.root.querySelector('[role=dialog]')).toBeNull()
  expect(shell.root.querySelector('#delegate-password')).not.toBeNull()
  await approve(shell.root,'secret')
  expect(passwords).toEqual(['secret'])
  expect(shell.content()?.textContent).toBe('App content')
  expect(shell.root.querySelector('#delegate-password')).toBeNull()
 }finally{shell.done()}
})

test('a refused password keeps the form, saying the password is wrong, and no content',async()=>{
 const {setup}=fakeSetup({state:'needs-admin-approval'},{state:'blocked',reason:'dsm_3121: DSM rejected the administrator password.',retry:true})
 const shell=await mount(setup)
 try{
  await approve(shell.root,'wrong')
  expect(shell.content()).toBeNull()
  expect(shell.root.textContent).toContain(delegateCopy('en').passwordRejected)
  expect(shell.root.textContent).not.toContain('dsm_3121')
  expect(shell.root.querySelector('#delegate-password')).not.toBeNull()
 }finally{shell.done()}
})

test('the window shows loading, not the content, until the first status arrives',async()=>{
 let answer:(s:CapabilityStatus)=>void=()=>{}
 const {setup}=fakeSetup(new Promise<CapabilityStatus>(r=>{answer=r}))
 const shell=await mount(setup)
 try{
  expect(shell.content()).toBeNull()
  expect(shell.root.textContent).toContain('Loading…')
  answer({state:'ready'});await settle()
  expect(shell.content()).not.toBeNull()
 }finally{shell.done()}
})

test('an approved executor shows the content at once',async()=>{
 const shell=await mount(fakeSetup({state:'ready'}).setup)
 try{
  expect(shell.content()).not.toBeNull()
  expect(shell.root.querySelector('#delegate-password')).toBeNull()
 }finally{shell.done()}
})

test('a later status that is not ready brings the approval back',async()=>{
 const {setup,publish}=fakeSetup({state:'ready'})
 const shell=await mount(setup)
 try{
  publish({state:'needs-admin-approval'});await settle()
  expect(shell.content()).toBeNull()
  expect(shell.root.querySelector('#delegate-password')).not.toBeNull()
 }finally{shell.done()}
})

test('reopening the window reads the executor again',async()=>{
 const {setup,publish}=fakeSetup({state:'ready'})
 let reopened=()=>{}
 const shell=await mount(setup,{admin:true,ctx:{reopen:(fn:()=>void)=>{reopened=fn;return ()=>{}}}})
 try{
  // Approval was withdrawn elsewhere; this window hears of it on reopen.
  publish({state:'needs-admin-approval'});await settle()
  publish({state:'ready'});await settle()
  expect(shell.content()).not.toBeNull()
  const status=setup.status
  setup.status=async()=>({state:'needs-admin-approval'})
  reopened();await settle()
  expect(shell.content()).toBeNull()
  setup.status=status
 }finally{shell.done()}
})

test('a user who is not an administrator is told to ask one, without a form or the content',async()=>{
 const shell=await mount(fakeSetup({state:'needs-admin-approval'}).setup,{admin:false})
 try{
  expect(shell.content()).toBeNull()
  expect(shell.root.querySelector('#delegate-password')).toBeNull()
  expect(shell.root.textContent).toContain(delegateCopy('en').consentAsk)
  // The same block stands before a root-ops App's pages.
  expect(shell.root.textContent).not.toMatch(/file/i)
 }finally{shell.done()}
})

test('without an executor there is nothing to approve',async()=>{
 const shell=await mount(undefined)
 try{expect(shell.content()).not.toBeNull()}finally{shell.done()}
})

// the executor did not start after approval; the status read
// needs approval again and the form came back with no word of the failure.
test('an approval after which the executor still needs approval says it failed, with the reason',async()=>{
 const copy=delegateCopy('en')
 const {setup}=fakeSetup({state:'needs-admin-approval'},{state:'needs-admin-approval',reason:'EXECUTOR_UNAVAILABLE'})
 const shell=await mount(setup)
 try{
  await approve(shell.root,'secret')
  expect(shell.content()).toBeNull()
  const alert=shell.root.querySelector('[role=alert]')!
  expect(alert.textContent).toContain(copy.setupError)
  expect(alert.textContent).toContain('EXECUTOR_UNAVAILABLE')
 }finally{shell.done()}
})

// a Portal page approving the executor named no App; a DSM window has its title bar.
test('on a full page the approval names the App with its icon',async()=>{
 const brand={displayName:'Demo App',appIcon:'/webman/3rdparty/Demo/icon.png',reopen:()=>()=>{}}
 const page=await mount(fakeSetup({state:'needs-admin-approval'}).setup,{admin:true,ctx:{...brand,fullPage:true}})
 try{
  expect(page.root.querySelector('.executor-approval')!.textContent).toContain('Demo App')
  expect(page.root.querySelector<HTMLImageElement>('.executor-approval img')!.getAttribute('src')).toBe(brand.appIcon)
 }finally{page.done()}
 const window=await mount(fakeSetup({state:'needs-admin-approval'}).setup,{admin:true,ctx:{...brand,fullPage:false}})
 try{expect(window.root.textContent).not.toContain('Demo App')}finally{window.done()}
})

// after an upgrade Reclaim's approval form came 2.5 s after its
// "Loading…", the time the form took to read the status the window had just
// read. The form starts from that status.
test('the approval form starts from the status the window read, without reading it again',async()=>{
 const {setup}=fakeSetup({state:'needs-admin-approval'})
 let reads=0
 const status=setup.status.bind(setup)
 setup.status=()=>{reads++;return status()}
 const shell=await mount(setup)
 try{
  expect(shell.root.querySelector('#delegate-password')).not.toBeNull()
  expect(reads).toBe(1)
 }finally{shell.done()}
})
