// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import BackgroundPanel from './BackgroundPanel.vue'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await Promise.resolve()}}
const owner={user:'alice',uid:'1026'}
const status={owner,admin:false,revision:1,worker:{username:'pkg',alive:true},limits:{maxInputBytes:1024,maxQueued:8,maxOwnerPending:4,maxOwnerBytes:4096,jobTimeoutSeconds:60}}
const job=(id:string,state:string,extra:object={})=>({id,name:id,state,submitter:owner,...extra})
function mountPanel(jobs:object[]){
 const call=vi.fn(async(method:string):Promise<unknown>=>method==='bg.status'?status:method==='bg.jobs.list'?{jobs}:method==='bg.schedules.list'?{schedules:[]}:{})
 const root=document.createElement('div');document.body.append(root)
 let exposed:{refresh():Promise<void>}|undefined
 const app=createApp({render:()=>h(AppRoot,{},()=>h(BackgroundPanel,{ctx:{call:call as never,locale:'en'},ref:(r:unknown)=>{exposed=r as typeof exposed}}))})
 app.mount(root)
 const row=(id:string)=>root.querySelector(`[data-job-id="${id}"]`)!
 const button=(id:string,label:string)=>Array.from(row(id).querySelectorAll('button')).find(b=>b.textContent===label)
 const control=(label:string)=>Array.from(root.querySelectorAll('button')).find(b=>b.textContent===label)
 return {call,row,button,control,refresh:()=>exposed!.refresh(),done(){app.unmount();root.remove()}}
}
// The DSM browser hides what a native <template> holds; jsdom keeps it queryable.
const shown=(el:Element|undefined)=>{for(let e:Element|null|undefined=el;e;e=e.parentElement)if(getComputedStyle(e).display==='none')return false;return !!el}
test('a running job pauses and a paused job resumes or cancels',async()=>{
 const v=mountPanel([job('run','running'),job('held','paused',{pauseReason:'user'})])
 try{
  await settle()
  expect(v.button('run','Resume')).toBeUndefined()
  v.button('run','Pause')!.click();await settle()
  expect(v.call).toHaveBeenCalledWith('bg.jobs.pause',{id:'run'})
  expect(v.row('held').textContent).toContain('Paused')
  expect(v.button('held','Pause')).toBeUndefined()
  expect(v.button('held','Cancel')).toBeDefined()
  v.button('held','Resume')!.click();await settle()
  expect(v.call).toHaveBeenCalledWith('bg.jobs.resume',{id:'held'})
 }finally{v.done()}
})
test('a delegated job offers no pause: its executor recovery cannot pause',async()=>{
 const v=mountPanel([job('dl','running',{delegation_id:'d1'})])
 try{
  await settle()
  expect(v.button('dl','Pause')).toBeUndefined()
  expect(v.button('dl','Cancel')).toBeDefined()
 }finally{v.done()}
})
test('a requested pause shows Pausing and can be withdrawn',async()=>{
 const v=mountPanel([job('run','running',{pauseRequested:true})])
 try{
  await settle()
  expect(v.row('run').textContent).toContain('Pausing')
  expect(v.button('run','Pause')).toBeUndefined()
  v.button('run','Resume')!.click();await settle()
  expect(v.call).toHaveBeenCalledWith('bg.jobs.resume',{id:'run'})
 }finally{v.done()}
})
test('a job shows its progress: items, bytes and the current item',async()=>{
 const v=mountPanel([
  job('run','running',{progress:{done:3,total:10,bytes:2048,totalBytes:1048576,item:'/photos/a.jpg',updatedAt:'2026-10-12T00:00:00Z'}}),
  job('one','paused',{progress:{done:0,total:1,bytes:0,totalBytes:0,updatedAt:'2026-10-12T00:00:00Z'}}),
 ])
 try{
  await settle()
  const run=v.row('run').textContent
  expect(run).toContain('3 of 10 items')
  expect(run).toContain('2 KB of 1 MB')
  expect(run).toContain('/photos/a.jpg')
  expect(v.row('one').textContent).toContain('0 of 1 item')
  expect(v.row('one').textContent).not.toContain(' B of ')
 }finally{v.done()}
})
test('a user sees the controls to choose a file and submit it',async()=>{
 const v=mountPanel([])
 try{
  await settle()
  expect(shown(v.control('Choose a file'))).toBe(true)
  expect(shown(v.control('Import and submit'))).toBe(true)
 }finally{v.done()}
})
// the page header refreshes; the panel keeps no Refresh button of its own.
test('the page header refreshes the panel, which has no Refresh button',async()=>{
 const v=mountPanel([])
 try{
  await settle()
  expect(v.control('Refresh')).toBeUndefined()
  const before=v.call.mock.calls.filter(([m])=>m==='bg.status').length
  await v.refresh();await settle()
  expect(v.call.mock.calls.filter(([m])=>m==='bg.status').length).toBe(before+1)
 }finally{v.done()}
})
// the item being copied is a path, so it is a PathDisplay.
test('the current item shows as PathDisplay',async()=>{
 const v=mountPanel([job('run','running',{progress:{done:1,total:2,bytes:0,totalBytes:0,item:'/photos/a.jpg'}})])
 try{await settle();expect(v.row('run').querySelector('.ui-path-display [data-ui-path-text][title="/photos/a.jpg"]')).not.toBeNull()}finally{v.done()}
})
// each job and schedule is a Card: its name is the title, its state a badge.
test('each job and schedule is a Card titled by its name with its state as a badge',async()=>{
 const v=mountPanel([job('report.csv','paused')])
 v.call.mockImplementation(async(method:string):Promise<unknown>=>method==='bg.status'?status:method==='bg.jobs.list'?{jobs:[job('report.csv','paused')]}:method==='bg.schedules.list'?{schedules:[{id:'s1',name:'nightly',owner,enabled:true,nextAt:'2026-10-14T00:00:00Z',intervalSeconds:86400,workerRevision:1,missed:0}]}:{})
 try{
  await settle();await v.refresh();await settle()
  for(const [card,title,state] of [[v.row('report.csv'),'report.csv','Paused'],[document.querySelector('[data-schedule-id="s1"]')!,'nightly','Enabled']] as const){
   expect(card.matches('[data-layout="compact"]')).toBe(true)
   expect(card.querySelector('h3')!.textContent!.trim()).toBe(title)
   expect(card.querySelector('header [data-bg-state]')!.textContent!.trim()).toBe(state)
  }
 }finally{v.done()}
})
