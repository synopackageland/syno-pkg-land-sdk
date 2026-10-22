// @vitest-environment jsdom
import {expect,test} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import ExecutorApproval from './ExecutorApproval.vue'
import {ExecutorSetupKey} from './use-executor-setup'
import type {CapabilityStatus,ExecutorSetup} from './executor-setup'
const settle=async()=>{for(let i=0;i<12;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
test('protected upgrade details precede PasswordConfirm and success restores the original page',async()=>{
 let current:CapabilityStatus={state:'needs-admin-approval',code:'task_outdated',approval:{package:'Example',old_policy_digest:'a'.repeat(64),new_policy_digest:'b'.repeat(64),new_executor_digest:'c'.repeat(64),permissions:'unchanged',task_template_changed:true,root_programs:[{name:'cli',old:'d'.repeat(64),new:'e'.repeat(64)}],policy_changes:[]}}
 const passwords:string[]=[]
 const setup:ExecutorSetup={status:async()=>current,authorize:async password=>{passwords.push(password);current={state:'ready'};return current},control:async()=>current}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorApproval,{admin:true,locale:'en'},()=>h('p',{'data-original-page':''},'Original page')))})
 app.provide(ExecutorSetupKey,setup);app.mount(root);await settle()
 try{
  const explanation=root.querySelector('[data-approval-diff]')!
  expect(explanation.textContent).toContain('System programs updated')
  expect(explanation.textContent).toContain('Approval task definitions updated')
  expect(explanation.textContent).toContain('Permissions unchanged')
  expect(explanation.textContent).toContain('dddddddddddd → eeeeeeeeeeee')
  expect(explanation.compareDocumentPosition(root.querySelector('form')!)&Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  const input=root.querySelector<HTMLInputElement>('input[type="password"]')!;input.value='fixture-password';input.dispatchEvent(new Event('input'));await settle();root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(passwords).toEqual(['fixture-password']);expect(root.querySelector('[data-original-page]')).not.toBeNull();expect(root.querySelector('[data-approval-diff]')).toBeNull()
 }finally{app.unmount();root.remove()}
})

test('an old runtime shows unknown and never claims unchanged permissions',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const status:CapabilityStatus={state:'needs-admin-approval',code:'task_outdated'}
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorApproval,{admin:true,locale:'en'},()=>h('p','Original page')))})
 app.provide(ExecutorSetupKey,{status:async()=>status,authorize:async()=>status,control:async()=>status});app.mount(root);await settle()
 try{const text=root.querySelector('[data-approval-diff]')?.textContent;expect(text).toContain('cannot be determined');expect(text).not.toContain('Permissions unchanged');expect(root.querySelector('[data-consent-title]')?.textContent).toBe('Approve this update')}
 finally{app.unmount();root.remove()}
})
