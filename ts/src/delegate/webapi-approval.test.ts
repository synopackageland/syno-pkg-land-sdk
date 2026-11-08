// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import ExecutorApproval from './ExecutorApproval.vue'
import ExecutorNotice from './ExecutorNotice.vue'
import {useScopeApproval} from './use-scope-approval'
import {ExecutorSetupKey} from './use-executor-setup'
import type {CapabilityStatus,ExecutorSetup} from './executor-setup'
import {delegateCopy} from './copy'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}

test.each([false,undefined,true])('WebAPI-only approval succeeds with missing delegate.status (declaration %s)',async fileDelegate=>{
 let current:CapabilityStatus={state:'needs-admin-approval'}
 const call=vi.fn(async()=>{throw {code:'unknown_method'}})
 const authorize=vi.fn(async()=>current={state:'ready'})
 const setup:ExecutorSetup={fileDelegate,status:async()=>current,authorize,control:async()=>current,call,
  webapiProfiles:{update:{purpose:'Read DSM update progress and server-check result',apis:{'SYNO.Core.Upgrade':{progress:[1]}}}}}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorApproval,{admin:true,locale:'zh-TW'},()=>h('p',{'data-content':''},'Approved')))})
 app.provide(ExecutorSetupKey,setup)
 try{
  app.mount(root);await settle()
  expect(root.querySelector('[role=alert]')).toBeNull()
  expect(root.textContent).toContain('讀取 DSM 更新進度與伺服器檢查結果')
  const input=root.querySelector<HTMLInputElement>('#delegate-password')!
  input.value='secret';input.dispatchEvent(new Event('input'));await settle()
  const button=root.querySelector<HTMLButtonElement>('button[type=submit]')!
  expect(button.disabled).toBe(false);button.click();await settle()
  expect(authorize).toHaveBeenCalledExactlyOnceWith('secret')
  expect(root.querySelector('[data-content]')).not.toBeNull()
  expect(call).toHaveBeenCalledTimes(fileDelegate===false?0:1)
 }finally{app.unmount();root.remove()}
})

test('the notice dialog uses the same manifest declaration and can approve without delegate calls',async()=>{
 let current:CapabilityStatus={state:'needs-admin-approval'}
 const call=vi.fn(async()=>{throw {code:'unknown_method'}})
 const authorize=vi.fn(async()=>current={state:'ready'})
 const executor:ExecutorSetup={fileDelegate:false,status:async()=>current,authorize,control:async()=>current,call}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorNotice,{locale:'en',ctx:{executor,call,user:{name:'admin',isAdmin:true}}}))})
 try{
  app.mount(root);await settle()
  Array.from(root.querySelectorAll('button')).find(b=>b.textContent?.trim()===delegateCopy('en').noticeApprove)!.click();await settle()
  const dialog=document.querySelector('[role=dialog]')!
  const input=dialog.querySelector<HTMLInputElement>('#delegate-password')!
  input.value='secret';input.dispatchEvent(new Event('input'));await settle()
  dialog.querySelector<HTMLButtonElement>('button[type=submit]')!.click();await settle()
  expect(authorize).toHaveBeenCalledExactlyOnceWith('secret');expect(call).not.toHaveBeenCalled()
  expect(root.textContent).not.toContain(delegateCopy('en').noticeAdmin)
 }finally{app.unmount();root.remove()}
})

test.each([false,undefined])('file-scope readback never waits for an absent delegate (declaration %s)',async fileDelegate=>{
 const blocked:CapabilityStatus={state:'blocked',requested:{scope:'home',generation:0}}
 // First load succeeds for the legacy host; the service disappears during readback.
 const call=vi.fn().mockResolvedValueOnce({}).mockRejectedValue({code:'unknown_method'})
 const setup:ExecutorSetup={fileDelegate,status:async()=>({state:'needs-admin-approval'}),authorize:async()=>blocked,control:async()=>blocked}
 let approval!:ReturnType<typeof useScopeApproval>
 const app=createApp({setup(){approval=useScopeApproval({admin:true,ctx:{call}},()=>{});return()=>null}})
 app.provide(ExecutorSetupKey,setup)
 const root=document.createElement('div')
 try{
  app.mount(root);await settle();approval.password.value='secret';await approval.authorize()
  expect(approval.busy.value).toBe(false);expect(approval.error.value).toBe(false)
  expect(approval.needsScope.value).toBe(false);expect(approval.canConfirm.value).toBe(true)
  expect(call).toHaveBeenCalledTimes(fileDelegate===false?0:2)
 }finally{app.unmount()}
})

test('unknown_method first encountered in landed ends readback immediately',async()=>{
 const blocked:CapabilityStatus={state:'blocked',requested:{scope:'home',generation:0}}
 const call=vi.fn().mockResolvedValueOnce({}).mockResolvedValueOnce({}).mockRejectedValue({code:'unknown_method'})
 const setup:ExecutorSetup={status:async()=>({state:'needs-admin-approval'}),authorize:async()=>blocked,control:async()=>blocked}
 let approval!:ReturnType<typeof useScopeApproval>
 const app=createApp({setup(){approval=useScopeApproval({admin:true,ctx:{call}},()=>{});return()=>null}})
 app.provide(ExecutorSetupKey,setup)
 try{app.mount(document.createElement('div'));await settle();approval.password.value='secret';await approval.authorize();expect(call).toHaveBeenCalledTimes(3);expect(approval.error.value).toBe(false)}finally{app.unmount()}
})
