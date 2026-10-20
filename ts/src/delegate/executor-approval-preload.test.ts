// @vitest-environment jsdom
import {expect,test} from 'vitest'
import {createApp,defineComponent,h,nextTick,onMounted,onBeforeUnmount} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import ExecutorApproval from './ExecutorApproval.vue'
import type {CapabilityStatus,ExecutorSetup} from './executor-setup'
import {ExecutorSetupKey} from './use-executor-setup'
import {delegateCopy} from './copy'
import AuthorizationGate from '../authorization/AuthorizationGate.vue'
import {authorizationCopy} from '../authorization/i18n'
import {useExecutorPreload} from './use-executor-setup'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}

// The approval's status answers when the test says; the App's page records
// its mount (when it would send its reads) and unmount.
function deferredSetup(){
 let answer!:(s:CapabilityStatus)=>void
 const setup:ExecutorSetup={status:()=>new Promise(r=>{answer=r}),async authorize(){return {state:'ready'}},async control(){return {state:'ready'}}}
 return {setup,answer:(s:CapabilityStatus)=>answer(s)}
}
async function mount(setup:ExecutorSetup|undefined,preload:boolean){
 const events:string[]=[]
 const Page=defineComponent({setup(){onMounted(()=>events.push('mounted'));onBeforeUnmount(()=>events.push('unmounted'));return()=>h('p',{class:'content'},'App content')}})
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorApproval,{admin:true,locale:'en',preload},()=>h(Page)))})
 if(setup)app.provide(ExecutorSetupKey,setup)
 app.mount(root);await settle()
 const pages=()=>root.querySelector<HTMLElement>('.content')?.closest<HTMLElement>('.executor-pages')
 return {root,events,pages,done:()=>{app.unmount();root.remove()}}
}

test('with preload the App mounts while the status is read, hidden and inert, and shows the same instance once ready',async()=>{
 const {setup,answer}=deferredSetup()
 const shell=await mount(setup,true)
 try{
  expect(shell.events).toEqual(['mounted'])
  expect(shell.pages()?.classList.contains('executor-preloading')).toBe(true)
  expect(shell.pages()?.hasAttribute('inert')).toBe(true)
  expect(shell.pages()?.getAttribute('aria-hidden')).toBe('true')
  expect(shell.root.textContent).toContain(delegateCopy('en').loading)
  answer({state:'ready'});await settle()
  expect(shell.events).toEqual(['mounted'])
  expect(shell.root.textContent).not.toContain(delegateCopy('en').loading)
  expect(shell.pages()?.classList.contains('executor-preloading')).toBe(false)
  expect(shell.pages()?.hasAttribute('inert')).toBe(false)
  expect(shell.pages()?.hasAttribute('aria-hidden')).toBe(false)
 }finally{shell.done()}
})

test('with preload a status that is not ready discards what the App loaded',async()=>{
 for(const state of ['needs-admin-approval','blocked'] as const){
  const {setup,answer}=deferredSetup()
  const shell=await mount(setup,true)
  try{
   expect(shell.events).toEqual(['mounted'])
   answer({state});await settle()
   expect(shell.events).toEqual(['mounted','unmounted'])
   expect(shell.root.querySelector('.content')).toBeNull()
   expect(shell.root.querySelector('.executor-approval')).not.toBeNull()
  }finally{shell.done()}
 }
})

test('without preload the App mounts only once the status reads ready, as before',async()=>{
 const {setup,answer}=deferredSetup()
 const shell=await mount(setup,false)
 try{
  expect(shell.events).toEqual([])
  answer({state:'ready'});await settle()
  expect(shell.events).toEqual(['mounted'])
  expect(shell.root.querySelector('.executor-pages')).toBeNull()
 }finally{shell.done()}
})

// A dialog leaves the hidden pages (it is teleported): the Gate's waits for
// the check, so nothing shows before the executor reads ready.
test('with preload the AuthorizationGate inside asks for nothing until the status reads ready',async()=>{
 const {setup,answer}=deferredSetup()
 const ctx={locale:'en',displayName:'Reclaim',reopen:()=>()=>{},authorization:{canManage:()=>true,status:async()=>({command:'authorization' as const,schema_version:1 as const,state:'authorization_required' as const,reason:'grant_missing'}),request:async()=>({command:'authorization' as const,schema_version:1 as const,state:'ready' as const}),revoke:async()=>({command:'authorization' as const,schema_version:1 as const,state:'ready' as const})}}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorApproval,{admin:true,locale:'en',preload:true},()=>h(AuthorizationGate,{ctx},()=>h('p',{class:'content'},'App content'))))})
 app.provide(ExecutorSetupKey,setup);app.config.idPrefix='preload-gate'
 app.mount(root);await settle()
 try{
  expect(root.querySelector('.content')).not.toBeNull()
  expect(document.querySelector('[role=dialog]')).toBeNull()
  answer({state:'ready'});await settle()
  expect(document.querySelector('[role=dialog]')?.textContent).toContain(authorizationCopy('en','Reclaim')('title'))
 }finally{app.unmount();root.remove()}
})

test('useExecutorPreload is true only while ExecutorApproval preloads its pages',async()=>{
 const seen:boolean[]=[]
 const Probe=defineComponent({setup(){const preloading=useExecutorPreload();return()=>{seen.push(preloading.value);return h('p',{class:'content'})}}})
 const {setup,answer}=deferredSetup()
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorApproval,{admin:true,locale:'en',preload:true},()=>h(Probe)))})
 app.provide(ExecutorSetupKey,setup);app.mount(root);await settle()
 try{
  expect(seen.at(-1)).toBe(true)
  answer({state:'ready'});await settle()
  expect(seen.at(-1)).toBe(false)
 }finally{app.unmount();root.remove()}
 const outside=createApp({render:()=>h(Probe)});const host=document.createElement('div');outside.mount(host)
 expect(seen.at(-1)).toBe(false);outside.unmount()
})

// 0.10.8 showed its loading text forever beside the pages when no setup was provided.
test('without an executor setup the pages show and no loading text does, with or without preload',async()=>{
 for(const preload of [false,true]){
  const shell=await mount(undefined,preload)
  try{
   expect(shell.root.textContent,String(preload)).toContain('App content')
   expect(shell.root.textContent,String(preload)).not.toContain(delegateCopy('en').loading)
   expect(shell.root.querySelector('.executor-preloading'),String(preload)).toBeNull()
  }finally{shell.done()}
 }
})
