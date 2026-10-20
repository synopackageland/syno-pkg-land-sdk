// @vitest-environment jsdom
// with Reclaim 1562 stopped, the window's approval showed the
// password form with "APP_STOPPED: the executor status request failed." and
// did not come back once the package ran again. A stopped or starting package
// waits for no approval: the approval and the settings panel say so and check
// again on their own until the executor answers.
import {afterEach,beforeEach,expect,test,vi} from 'vitest'
import {createApp,h,type Component} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import ExecutorApproval from './ExecutorApproval.vue'
import ExecutorSettings from './ExecutorSettings.vue'
import ExecutorNotice from './ExecutorNotice.vue'
import type {CapabilityStatus,ExecutorSetup} from './executor-setup'
import {ExecutorSetupKey} from './use-executor-setup'
import {lifecycleCopy} from './lifecycle'

// What the DSM window's access reads (cli root-window.ts) while the App's
// executor.status refuses with the package's lifecycle code.
const refused=(code:string):CapabilityStatus=>({state:'blocked',code:'executor_unreachable',reason:`${code}: the executor status request failed.`,retry:true})
const stopped=refused('APP_STOPPED'),starting=refused('APP_STARTING')
const ready:CapabilityStatus={state:'ready'}

// The package as time goes on: stopped until start, starting until up, then ready.
function packageSetup(timeline:{start:number;up:number}){
 const begin=Date.now()
 let reads=0
 const setup:ExecutorSetup={
  async status(){reads++;const at=Date.now()-begin;return at<timeline.start?stopped:at<timeline.up?starting:ready},
  async authorize(){return ready},
  async control(){return ready},
 }
 return {setup,reads:()=>reads}
}

const cleanup:Array<()=>void>=[]
beforeEach(()=>{vi.useFakeTimers()})
afterEach(()=>{cleanup.splice(0).forEach(fn=>fn());vi.useRealTimers()})
async function mount(component:Component,props:Record<string,unknown>,setup:ExecutorSetup){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(component,{admin:true,locale:'en',...props},()=>h('p',{class:'content'},'App content')))})
 app.provide(ExecutorSetupKey,setup)
 app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 await vi.advanceTimersByTimeAsync(0)
 return root
}
const text=(root:Element)=>root.textContent??''

for(const [name,component,shown,props] of [
 ['ExecutorApproval',ExecutorApproval,(root:Element)=>!!root.querySelector('.content'),{}],
 ['ExecutorSettings',ExecutorSettings,(root:Element)=>!!root.querySelector('[data-executor-status="ready"]'),{}],
 ['ExecutorNotice',ExecutorNotice,(root:Element)=>!root.querySelector('.executor-notice'),{ctx:{user:{isAdmin:true}}}],
] as const){
 test(`${name}: a stopped package is said to be stopped, with no password form`,async()=>{
  const root=await mount(component,props,packageSetup({start:Infinity,up:Infinity}).setup)
  expect(text(root)).toContain(lifecycleCopy('stopped','en'))
  expect(root.querySelector('#delegate-password')).toBeNull()
  expect(text(root)).not.toContain('APP_STOPPED')
  expect(shown(root)).toBe(false)
 })

 test(`${name}: a starting package is said to be starting`,async()=>{
  const root=await mount(component,props,packageSetup({start:0,up:Infinity}).setup)
  expect(text(root)).toContain(lifecycleCopy('starting','en'))
  expect(root.querySelector('#delegate-password')).toBeNull()
 })

 // Stopped for a minute, so the checks have backed off to their ceiling; the
 // start step then runs 15.5 s. Ready within 20 s of the start, unrefreshed,
 // whenever the start falls between two checks.
 test(`${name}: after a start it shows ready within 20 s, by itself`,async()=>{
  for(let start=50_000;start<66_000;start+=500){
   const up=start+15_500
   const root=await mount(component,props,packageSetup({start,up}).setup)
   await vi.advanceTimersByTimeAsync(start)
   expect(shown(root)).toBe(false)
   await vi.advanceTimersByTimeAsync(up-start)
   let waited=up-start
   while(!shown(root)&&waited<40_000){await vi.advanceTimersByTimeAsync(250);waited+=250}
   expect(waited,`start ${start}`).toBeLessThanOrEqual(20_000)
   expect(root.querySelector('#delegate-password')).toBeNull()
   cleanup.splice(0).forEach(fn=>fn())
  }
 })
}

test('a ready executor is not read again on a timer',async()=>{
 const pkg=packageSetup({start:0,up:0})
 await mount(ExecutorApproval,{},pkg.setup)
 const reads=pkg.reads()
 await vi.advanceTimersByTimeAsync(120_000)
 expect(pkg.reads()).toBe(reads)
})
