// @vitest-environment jsdom
import {expect,test} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import BackgroundSavePanel from './BackgroundSavePanel.vue'
import type {BackgroundSaveFacade} from './long-running'
import {saveCopy} from './save-copy'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
// a folder choice that Application Privileges refuse closes the
// picker; the panel says the folder was not authorized, or the App's text.
async function refusedChoice(extra:Record<string,unknown>={}){
 const sdk={async status(){return {disabled:false}},async list(){return []},async operations(){return []},async destination(){return {status:'none'}},async selectDestination(){throw {code:'APP_DENIED'}}} as unknown as BackgroundSaveFacade
 const provider={async shares(){return {entries:[{name:'home',path:'/home',isDir:true}],total:1,offset:0}},async list(){return {entries:[],total:0,offset:0}},async info(){throw {code:'not_found'}},async createFolder(){},dispose(){}}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(BackgroundSavePanel,{sdk,admin:false,locale:'en',picker:{provider,keys:['save']},...extra}))})
 app.mount(root);await settle()
 const button=(label:string)=>Array.from(document.querySelectorAll('button')).find(b=>b.textContent?.trim()===label)!
 button(saveCopy('en').choose).click();await settle()
 document.querySelector('tr[data-row-key="/home"]')!.dispatchEvent(new MouseEvent('click',{bubbles:true}));await settle()
 button('Confirm selection').click();await settle()
 return {root,done:()=>{app.unmount();root.remove()}}
}
test('a refused folder choice closes the picker and is not reported as a refused save',async()=>{
 const copy=saveCopy('en'),panel=await refusedChoice()
 try{
  expect(document.querySelector('tr[data-row-key="/home"]')).toBeNull()
  expect(panel.root.querySelector('[role=alert] [data-save-error]')?.textContent).toBe(copy.appPrivilegeSelect)
 }finally{panel.done()}
})
test('an App sets its own text for a refused folder choice',async()=>{
 const panel=await refusedChoice({pickerErrorText:(e:{code?:string})=>e.code==='APP_DENIED'?'Gopeed: not from this address':undefined})
 try{expect(panel.root.querySelector('[role=alert] [data-save-error]')?.textContent).toBe('Gopeed: not from this address')}
 finally{panel.done()}
})
// the panel reads as status, details and actions, never internal ids.
async function panel(over:Partial<Record<keyof BackgroundSaveFacade,unknown>>={},props:Record<string,unknown>={}){
 const calls={status:0}
 const sdk={async status(){calls.status++;return {disabled:false,selection:{scope:'shared:fgwmrf',root:'/fgwmrf',generation:1}}},async list(){return []},async operations(){return []},async destination(){return {key:'save',status:'active',selection:null,destination:{id:'g',key:'save',path:'/fgwmrf/t0383',username:'a',lifetime:'until-revoked'}}},...over} as unknown as BackgroundSaveFacade
 const root=document.createElement('div');document.body.append(root)
 let exposed:{refresh():Promise<void>}|undefined
 const app=createApp({render:()=>h(AppRoot,{},()=>h(BackgroundSavePanel,{sdk,admin:true,locale:'zh-TW',ref:(r:unknown)=>{exposed=r as typeof exposed},...props}))})
 app.mount(root);await settle()
 const buttons=()=>Array.from(root.querySelectorAll('button')).map(b=>b.textContent!.trim())
 return {root,calls,buttons,refresh:()=>exposed!.refresh(),done:()=>{app.unmount();root.remove()}}
}
const provider={async shares(){return {entries:[],total:0,offset:0}},async list(){return {entries:[],total:0,offset:0}},async info(){throw {code:'not_found'}},async createFolder(){},dispose(){}}
test('the page refreshes the panel; the panel has no refresh button of its own',async()=>{
 const p=await panel()
 try{
  expect(p.buttons()).not.toContain('重新整理')
  const before=p.calls.status
  await p.refresh();await settle()
  expect(p.calls.status).toBe(before+1)
 }finally{p.done()}
})
test('a failed load offers a retry inside its message',async()=>{
 const copy=saveCopy('zh-TW')
 let fail=true
 const p=await panel({async status(){if(fail)throw {code:'unavailable'};return {disabled:false}}})
 try{
  const retry=p.root.querySelector('[role=alert] button') as HTMLButtonElement
  expect(retry.textContent!.trim()).toBe(copy.retry)
  fail=false;retry.click();await settle()
  expect(p.root.querySelector('[role=alert]')).toBeNull()
 }finally{p.done()}
})
// saving a sample file demonstrates the flow in templates and sample
// Apps; a shipped App does not show it unless it opts in with sampleSave.
test('the sample save shows only when the App opts in with sampleSave',async()=>{
 const copy=saveCopy('zh-TW')
 const off=await panel({},{picker:{provider,keys:['save']}})
 try{
  expect(off.buttons()).not.toContain(copy.example)
  expect(off.root.textContent).not.toContain(copy.exampleHelp)
 }finally{off.done()}
 const on=await panel({},{picker:{provider,keys:['save']},sampleSave:true})
 try{
  expect(on.buttons()).toContain(copy.example)
  expect(on.root.textContent).toContain(copy.exampleHelp)
 }finally{on.done()}
})
test('the chosen folder remains a PathDisplay',async()=>{
 const p=await panel({},{picker:{provider,keys:['save']}})
 try{expect(p.root.querySelector('[data-save-row="folder"] .ui-path-display [data-ui-path-text][data-ui-tooltip-text="/fgwmrf/t0383"]')).not.toBeNull()}finally{p.done()}
})

test('the sample producer sends once while busy, reports refusal, and clears it on refresh',async()=>{
 let sends=0,fail:(error:unknown)=>void=()=>{}
 const copy=saveCopy('en')
 const p=await panel({exportExample(){sends++;return new Promise((_,reject)=>{fail=reject})}},{locale:'en',picker:{provider,keys:['save']},sampleSave:true})
 try{
  const sample=Array.from(p.root.querySelectorAll<HTMLButtonElement>('button')).find(b=>b.textContent?.trim()===copy.example)!
  sample.click();await settle();sample.click();await settle()
  expect(sends).toBe(1)
  fail({code:'PERMISSION_DENIED'});await settle()
  expect(p.root.querySelector('[role=alert]')?.textContent).toContain(copy.permission)
  expect(p.root.querySelector('[data-save-operations]')).toBeNull()
  await p.refresh();await settle()
  expect(p.root.querySelector('[role=alert]')).toBeNull()
 }finally{p.done()}
})
