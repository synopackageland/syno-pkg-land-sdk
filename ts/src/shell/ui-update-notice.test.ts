// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import AppShell from './AppShell.vue'
import UiUpdateNotice from './UiUpdateNotice.vue'
import {shellCopy} from './i18n'
import type {Context} from '../context'
const settle=async()=>{for(let i=0;i<6;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
const copy=shellCopy('en')
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks()})

function reopenEvents(){
 const handlers:Array<()=>void>=[]
 return {reopen:(fn:()=>void)=>{handlers.push(fn);return ()=>{handlers.splice(handlers.indexOf(fn),1)}},emit:()=>handlers.slice().forEach(fn=>fn()),handlers}
}
async function shell(ctx:Partial<Context>){
 const root=document.createElement('div');document.body.append(root)
 const full={fullPage:false,locale:'en',call:async<T,>()=>({}) as T,reopen:()=>()=>{},...ctx}
 const app=createApp({render:()=>h(AppShell,{ctx:full,locale:'en',modelValue:'home',pages:[{id:'home',title:'Home'}]},()=>h('p',{class:'content'},'App content'))})
 app.mount(root);await settle()
 return {root,reload:()=>Array.from(root.querySelectorAll('button')).find(b=>b.textContent?.trim()===copy.uiReload),done:()=>{app.unmount();root.remove()}}
}

test('a page whose UI the installed package replaced offers a reload above the content',async()=>{
 const check=vi.fn(async()=>true)
 const reload=vi.fn()
 vi.spyOn(window,'location','get').mockReturnValue({...window.location,reload} as Location)
 const s=await shell({uiUpdate:{check}})
 try{
  expect(check).toHaveBeenCalledTimes(1)
  expect(s.root.textContent).toContain(copy.uiUpdated)
  expect(s.root.querySelector('.content')).not.toBeNull()
  // One line, a status rather than an alert: nothing is held back.
  expect(s.root.querySelector('[role=status]')?.textContent).toContain(copy.uiUpdated)
  s.reload()!.click()
  expect(reload).toHaveBeenCalledTimes(1)
 }finally{s.done()}
})

test('a current UI, a failed check and an older window show nothing',async()=>{
 for(const ctx of [{uiUpdate:{check:async()=>false}},{uiUpdate:{check:async()=>{throw new Error('offline')}}},{}] as Partial<Context>[]){
  const s=await shell(ctx)
  try{
   expect(s.root.textContent).not.toContain(copy.uiUpdated)
   expect(s.reload()).toBeUndefined()
  }finally{s.done()}
 }
})

test('a reopened window checks again and stops once the page is known stale',async()=>{
 const events=reopenEvents()
 let stale=false
 const check=vi.fn(async()=>stale)
 const s=await shell({uiUpdate:{check},reopen:events.reopen})
 try{
  expect(s.root.textContent).not.toContain(copy.uiUpdated)
  stale=true;events.emit();await settle()
  expect(check).toHaveBeenCalledTimes(2)
  expect(s.root.textContent).toContain(copy.uiUpdated)
  events.emit();await settle()
  expect(check).toHaveBeenCalledTimes(2)
 }finally{s.done()}
 expect(events.handlers).toHaveLength(0)
})

test('a page shown again checks at most once a minute',async()=>{
 vi.useFakeTimers({now:0})
 const check=vi.fn(async()=>false)
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(UiUpdateNotice,{ctx:{uiUpdate:{check}},locale:'en'})})
 app.mount(root);await vi.advanceTimersByTimeAsync(0)
 const visible=()=>document.dispatchEvent(new Event('visibilitychange'))
 try{
  expect(check).toHaveBeenCalledTimes(1)
  visible();await vi.advanceTimersByTimeAsync(0)
  expect(check).toHaveBeenCalledTimes(1)
  await vi.advanceTimersByTimeAsync(60_000)
  visible();await vi.advanceTimersByTimeAsync(0)
  expect(check).toHaveBeenCalledTimes(2)
 }finally{app.unmount();root.remove()}
 // Unmounted: no more checks, and an in-flight one is aborted.
 await vi.advanceTimersByTimeAsync(60_000);visible();await vi.advanceTimersByTimeAsync(0)
 expect(check).toHaveBeenCalledTimes(2)
 expect((check.mock.calls[1] as unknown as [AbortSignal])[0].aborted).toBe(true)
})

test('every shell catalog has the reload notice',async()=>{
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
  const c=shellCopy(locale)
  expect(c.uiUpdated,locale).toBeTruthy();expect(c.uiReload,locale).toBeTruthy()
  if(locale!=='en')expect(c.uiUpdated,locale).not.toBe(copy.uiUpdated)
  expect(c.uiUpdated,locale).not.toMatch(/[.。]$/)
 }
 expect(shellCopy('zh-TW').uiUpdated).toBe('已更新，請重新整理頁面')
})
