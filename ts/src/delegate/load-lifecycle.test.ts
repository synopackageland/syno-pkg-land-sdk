// @vitest-environment jsdom
// a panel whose first load a stopped or starting package refuses says
// so, as the other panels do, and loads again on its own until the package
// answers; a start shows within 20 s. Its reads never show an error for it.
import {afterEach,beforeEach,expect,test,vi} from 'vitest'
import {createApp,h,type Component} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import BackgroundPanel from '../background/BackgroundPanel.vue'
import FilesPage from '../files/FilesPage.vue'
import {lifecycleCopy} from './lifecycle'

// The package as time goes on: stopped until start, starting until up, then answering.
function packageCall(timeline:{start:number;up:number},answer:(method:string)=>unknown){
 const begin=Date.now()
 return async(method:string)=>{
  const at=Date.now()-begin
  if(at<timeline.start)throw {code:'APP_STOPPED'}
  if(at<timeline.up)throw {code:'APP_STARTING'}
  return answer(method)
 }
}
const status={admin:false,revision:1,worker:null,limits:{maxInputBytes:1024,maxQueued:8,maxOwnerPending:4,maxOwnerBytes:4096,jobTimeoutSeconds:60}}
const background=(method:string)=>method==='bg.status'?status:method==='bg.jobs.list'?{jobs:[]}:{schedules:[]}
const files=(method:string)=>method==='nas.session.status'?{}:method==='nas.files.shares'?{shares:[],offset:0,total:0}:{files:[],offset:0,total:0}

const panels:Array<[string,Component,(call:(method:string)=>Promise<unknown>)=>Record<string,unknown>,(root:Element)=>boolean]>=[
 ['BackgroundPanel',BackgroundPanel,call=>({ctx:{call,locale:'en'}}),root=>!root.querySelector('[data-placeholder-row],[role="alert"]')&&!/stopped|starting/.test(text(root))],
 ['FilesPage',FilesPage,call=>({ctx:{call,locale:'en',reopen:()=>()=>{}}}),root=>!!root.querySelector('.files-actions')],
]

const cleanup:Array<()=>void>=[]
beforeEach(()=>{vi.useFakeTimers()})
afterEach(()=>{cleanup.splice(0).forEach(fn=>fn());vi.useRealTimers()})
async function mount(component:Component,props:Record<string,unknown>){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(component,props))})
 app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 await vi.advanceTimersByTimeAsync(0)
 return root
}
const text=(root:Element)=>root.textContent??''

for(const [name,component,props,shown] of panels){
 const answer=name==='BackgroundPanel'?background:files
 test(`${name}: a stopped package is said to be stopped, with no error`,async()=>{
  const root=await mount(component,props(packageCall({start:Infinity,up:Infinity},answer)))
  expect(text(root)).toContain(lifecycleCopy('stopped','en'))
  expect(root.querySelector('[role="alert"]')).toBeNull()
  expect(shown(root)).toBe(false)
 })

 test(`${name}: a starting package is said to be starting`,async()=>{
  const root=await mount(component,props(packageCall({start:0,up:Infinity},answer)))
  expect(text(root)).toContain(lifecycleCopy('starting','en'))
  expect(root.querySelector('[role="alert"]')).toBeNull()
 })

 // Stopped for a minute, then a 15.5 s start step: loaded within 20 s of the
 // start, unrefreshed, whenever the start falls between two loads.
 test(`${name}: after a start it loads within 20 s, by itself`,async()=>{
  for(let start=50_000;start<66_000;start+=500){
   const up=start+15_500
   const root=await mount(component,props(packageCall({start,up},answer)))
   await vi.advanceTimersByTimeAsync(up)
   let waited=up-start
   while(!shown(root)&&waited<40_000){await vi.advanceTimersByTimeAsync(250);waited+=250}
   expect(waited,`start ${start}`).toBeLessThanOrEqual(20_000)
   expect(text(root)).not.toContain(lifecycleCopy('starting','en'))
   cleanup.splice(0).forEach(fn=>fn())
  }
 })
}
