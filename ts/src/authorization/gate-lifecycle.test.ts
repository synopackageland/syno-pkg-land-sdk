// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import AuthorizationGate from './AuthorizationGate.vue'
import {authorizationCopy} from './i18n'
import {appRefusal} from './refusal'
import type {AuthorizationAccess,AuthorizationStatus} from '../context'
const cleanup:Array<()=>void>=[]
afterEach(()=>{cleanup.splice(0).forEach(fn=>fn());vi.useRealTimers()})
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
const reply=(state:AuthorizationStatus['state'],reason?:string):AuthorizationStatus=>({command:'authorization',schema_version:1,state,...(reason?{reason}:{})})
function mount(authorization:AuthorizationAccess,displayName:string|undefined='Reclaim'){
 const root=document.createElement('div');document.body.append(root)
 const ctx={locale:'en',displayName,authorization,reopen:()=>()=>{}}
 const app=createApp({render:()=>h(AppRoot,{},()=>h(AuthorizationGate,{ctx},()=>h('p',{class:'content'},'app content')))})
 app.config.idPrefix='gate-lifecycle';app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 return root
}
const text=authorizationCopy('en','Reclaim')
const dialog=()=>document.querySelector<HTMLElement>('[role=dialog]')
const failing=(error:unknown)=>({canManage:()=>true,status:async()=>{throw error},request:vi.fn(),revoke:vi.fn()})

// a stopped package is not an authorization to approve; the Gate
// says so and closes by itself once the package runs.
test('a stopped package says so and the dialog closes by itself once it runs',async()=>{
 vi.useFakeTimers({toFake:['setTimeout']})
 let stopped=true
 const status=vi.fn(async()=>{if(stopped)throw {code:'APP_STOPPED'};return reply('ready')})
 mount({canManage:()=>true,status,request:vi.fn(),revoke:vi.fn()})
 await vi.advanceTimersByTimeAsync(4000);await settle()
 expect(dialog()?.textContent).toContain(text('stopped.title'))
 expect(dialog()?.textContent).not.toContain(text('unavailable.body'))
 expect(dialog()?.textContent).not.toContain(text('authorize'))
 stopped=false
 await vi.advanceTimersByTimeAsync(15000);await settle()
 expect(dialog()).toBeNull()
})
// stopped for a minute, the checks back off to 15 s; a start step
// of 15.5 s then ends, and the Gate closes within 20 s of the start, whenever
// the start falls between two checks.
// Each phase has its own Vue app, fake timers and test deadline. The whole
// phase sweep must not share a five-second wall-clock test budget.
test.each(Array.from({length:32},(_,step)=>50_000+step*500))('after a start the dialog closes within 20 s, by itself (phase %i ms)',async(start)=>{
 vi.useFakeTimers({toFake:['setTimeout','Date']})
 const begin=Date.now(),up=start+15_500
 const status=vi.fn(async()=>{const at=Date.now()-begin;if(at<start)throw {code:'APP_STOPPED'};if(at<up)throw {code:'APP_STARTING'};return reply('ready')})
 mount({canManage:()=>true,status,request:vi.fn(),revoke:vi.fn()})
 await vi.advanceTimersByTimeAsync(up);await settle()
 let waited=up-start
 while(dialog()&&waited<40_000){await vi.advanceTimersByTimeAsync(250);await settle();waited+=250}
 expect(waited,`start ${start}`).toBeLessThanOrEqual(20_000)
})
test('a starting package reads as starting',async()=>{
 vi.useFakeTimers({toFake:['setTimeout']})
 mount({canManage:()=>true,status:async()=>reply('unavailable','APP_STARTING'),request:vi.fn(),revoke:vi.fn()})
 await settle();await vi.advanceTimersByTimeAsync(4100);await settle()
 expect(dialog()?.textContent).toContain(text('starting.title'))
})

// P14: only a temporary failure is called temporary.
test('a refused or misconfigured check is not called temporary',async()=>{
 const cases:Array<[unknown,string]>=[
  [{code:'bad_response',message:'HTTP 403'},'denied'],
  [{code:'app_privilege_denied'},'app_privilege_denied'],
  [{code:'admin_required'},'admin_required'],
  [{code:'PERMISSION_DENIED'},'denied'],
  [{code:'cross_origin'},'denied'],
  [{code:'unknown_method'},'config'],
  [{code:'authorization_bad_response'},'config'],
  [{code:'request_timeout'},'unavailable'],
 ]
 vi.useFakeTimers({toFake:['setTimeout']})
 for(const [error,key] of cases){
  mount(failing(error))
  await settle();await vi.advanceTimersByTimeAsync(4100);await settle()
  expect(dialog()?.textContent,JSON.stringify(error)).toContain(appRefusal(error,'en','Reclaim')?.message??text(key+'.body'))
  if(key!=='unavailable')expect(dialog()?.textContent).not.toContain(text('unavailable.body'))
  // "Did not finish, try again" is for a temporary failure; a refusal's body says what to do.
  if(key!=='unavailable')expect(dialog()?.textContent).not.toContain(text('failed'))
  cleanup.splice(0).forEach(fn=>fn())
 }
})
test('every new key is translated',()=>{
 for(const locale of ['zh-TW','zh-CN','ja','ko','de','fr','es','it'])for(const key of ['stopped.title','stopped.body','starting.title','starting.body','denied.title','denied.body','config.title','config.body']){
  const t=authorizationCopy(locale,'R')(key)
  expect(t,`${locale} ${key}`).not.toBe(key)
  expect(t,`${locale} ${key}`).not.toBe(authorizationCopy('en','R')(key))
 }
})

test('a status reason retains the specific App refusal and uses the App name',async()=>{
 vi.useFakeTimers({toFake:['setTimeout']})
 for(const code of ['app_privilege_denied','admin_required']){
  mount({canManage:()=>false,status:async()=>reply('unavailable',code),request:vi.fn(),revoke:vi.fn()})
  await settle();await vi.advanceTimersByTimeAsync(4100);await settle()
  expect(dialog()?.textContent).toContain(appRefusal({code},'en','Reclaim')!.message)
  cleanup.splice(0).forEach(fn=>fn())
 }
 mount(failing({code:'app_privilege_denied'}),'')
 await settle();await vi.advanceTimersByTimeAsync(4100);await settle()
 expect(dialog()?.textContent).toContain('select this app and click Edit')
})
