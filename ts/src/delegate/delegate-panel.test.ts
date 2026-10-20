// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import DelegatePanel from './DelegatePanel.vue'
import {delegateCopy} from './copy'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
function mountPanel(){
 const call=vi.fn(async(method:string):Promise<unknown>=>method==='delegate.status'?{state:'ready'}:method==='delegate.list'?{delegations:[]}:{jobs:[]})
 const root=document.createElement('div');document.body.append(root)
 let exposed:{refresh():Promise<void>}|undefined
 const app=createApp({render:()=>h(AppRoot,{},()=>h(DelegatePanel,{ctx:{call:call as never,locale:'zh-TW'},ref:(r:unknown)=>{exposed=r as typeof exposed}}))})
 app.mount(root)
 return {root,call,refresh:()=>exposed!.refresh(),done(){app.unmount();root.remove()}}
}
// one refresh on the page header, fields above their controls.
test('the page header refreshes the panel, which has no Refresh button',async()=>{
 const v=mountPanel()
 try{
  await settle()
  expect(Array.from(v.root.querySelectorAll('button')).map(b=>b.textContent!.trim())).not.toContain('重新整理')
  const before=v.call.mock.calls.filter(([m])=>m==='delegate.jobs.list').length
  await v.refresh();await settle()
  expect(v.call.mock.calls.filter(([m])=>m==='delegate.jobs.list').length).toBe(before+1)
 }finally{v.done()}
})
test('fields stack above their controls',async()=>{
 const v=mountPanel()
 try{await settle();expect(v.root.querySelector('form')!.querySelector('[data-direction="vertical"],.flex-col')).not.toBeNull()}finally{v.done()}
})
// a delegated folder is a PathDisplay.
test('a delegated folder shows as PathDisplay',async()=>{
 const v=mountPanel()
 v.call.mockImplementation(async(method:string):Promise<unknown>=>method==='delegate.status'?{state:'ready'}:method==='delegate.list'?{delegations:[{delegation_id:'d',job_id:'j',path:'/downloads/films',expires_at:'2026-10-14T00:00:00Z'}]}:{jobs:[]})
 try{await settle();await v.refresh();await settle();expect(v.root.querySelector('.ui-path-display [data-ui-path-text][title="/downloads/films"]')).not.toBeNull()}finally{v.done()}
})
// DSM lists jobs as rows with the state in its own column, not strung onto the name.
test('a job row shows its name and its state as a badge, and a delegation when it expires',async()=>{
 const copy=delegateCopy('zh-TW'),v=mountPanel()
 v.call.mockImplementation(async(method:string):Promise<unknown>=>method==='delegate.status'?{state:'ready'}:method==='delegate.list'?{delegations:[{delegation_id:'d',job_id:'j',path:'/downloads/films',expires_at:'2026-10-14T00:00:00Z'}]}:{jobs:[{id:'a',name:'a.iso',state:'running'},{id:'b',name:'b.pdf',state:'failed'}]})
 try{
  await settle();await v.refresh();await settle()
  const rows=Array.from(v.root.querySelectorAll('[role=list] > [role=listitem]'))
  expect(rows.map(row=>row.querySelector('[data-ui-list-item-title]')?.textContent?.trim())).toEqual(['a.iso','b.pdf',expect.stringContaining('/downloads/films')])
  expect(rows.slice(0,2).map(row=>row.querySelector('[data-ui-list-item-meta] [data-job-badge]')?.textContent?.trim())).toEqual([copy.running,copy.failed])
  expect(rows[0].querySelector('[data-ui-list-item-actions]')?.textContent).toContain(copy.cancel)
  expect(rows[1].querySelector('[data-ui-list-item-actions]')).toBeNull()
  expect(rows[2].querySelector('[data-ui-list-item-description]')?.textContent).toContain(copy.expires)
  expect(v.root.textContent).not.toContain('·')
 }finally{v.done()}
})
// delegate.status refuses with APP_STOPPED, APP_STARTING or
// EXECUTOR_STARTING since 0.10.5, which the panel showed as an error. It says
// the package is stopped or starting, as ExecutorNotice does, and comes back
// by itself.
for(const [code,key] of [['APP_STOPPED','appStopped'],['APP_STARTING','appStarting'],['EXECUTOR_STARTING','appStarting']] as const)test(`a ${code} refusal says so, not as an error, and the panel comes back by itself`,async()=>{
 vi.useFakeTimers({shouldAdvanceTime:true})
 const copy=delegateCopy('zh-TW')
 const v=mountPanel()
 await settle()
 let down=true
 v.call.mockImplementation(async(method:string):Promise<unknown>=>{if(down)throw {code};return method==='delegate.status'?{state:'ready'}:method==='delegate.list'?{delegations:[]}:{jobs:[]}})
 try{
  await v.refresh();await settle()
  expect(v.root.textContent).toContain(copy[key])
  expect(v.root.querySelector('[role=alert]')).toBeNull()
  down=false
  await vi.advanceTimersByTimeAsync(2000);await settle()
  expect(v.root.textContent).not.toContain(copy[key])
  expect(v.root.querySelector<HTMLInputElement>('#delegate-url')!.disabled).toBe(false)
 }finally{v.done();vi.useRealTimers()}
})
