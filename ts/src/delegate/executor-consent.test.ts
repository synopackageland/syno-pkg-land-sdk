// @vitest-environment jsdom
// the approval says why before it asks for the password: the App is
// not Synology's, DSM gives third-party Apps no system privileges, what the
// App needs them for; what approving does waits behind a disclosure, closed
// at first. Errors stay at the field.
import {expect,test} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import ExecutorApproval from './ExecutorApproval.vue'
import ExecutorNotice from './ExecutorNotice.vue'
import ExecutorSettings from './ExecutorSettings.vue'
import type {CapabilityStatus,ExecutorSetup} from './executor-setup'
import {ExecutorSetupKey} from './use-executor-setup'
import {delegateCatalogs,delegateCopy} from './copy'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
const copy=delegateCopy('en')

function access(answer:(password:string)=>CapabilityStatus=()=>({state:'ready'})){
 const passwords:string[]=[]
 let current:CapabilityStatus={state:'needs-admin-approval'}
 const setup:ExecutorSetup={status:async()=>current,async authorize(password){passwords.push(password);const next=answer(password);if(next.state==='ready')current=next;return next},async control(){return current}}
 return {setup,passwords}
}
async function mount(render:()=>ReturnType<typeof h>,setup:ExecutorSetup){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},render)})
 app.provide(ExecutorSetupKey,setup)
 app.mount(root);await settle()
 const field=()=>root.querySelector('#delegate-password')?.closest('[data-form-item]')
 const type=async(value:string)=>{const input=root.querySelector<HTMLInputElement>('#delegate-password')!;input.value=value;input.dispatchEvent(new Event('input'));await settle()}
 const submit=async()=>{root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()}
 return {root,field,type,submit,done:()=>{app.unmount();root.remove()}}
}
const approval=(admin:boolean,setup:ExecutorSetup,purpose?:string)=>mount(()=>h(ExecutorApproval,{admin,locale:'en',purpose},()=>h('p','App content')),setup)

test('an administrator reads why and what it is for before the password; the rest waits closed',async()=>{
 const p=await approval(true,access().setup)
 try{
  const consent=p.root.querySelector('[data-executor-consent]')!
  expect(consent.classList).toContain('executor-consent-page')
  expect(consent.querySelector('[data-consent-title]')!.textContent).toBe(copy.consentTitle)
  const lead=consent.querySelector('[data-consent-lead]')!.textContent
  for(const sentence of [copy.consentWhy,copy.consentPurpose])expect(lead).toContain(sentence)
  // The password's own note sits at the field: where it goes.
  expect(p.field()!.textContent).toContain(copy.passwordNote)
  expect(consent.contains(p.root.querySelector('form'))).toBe(true)
  const more=consent.querySelector<HTMLDetailsElement>('details[data-consent-points]')!
  expect(more.open).toBe(false)
  expect(more.querySelector('summary')!.textContent!.trim()).toBe(copy.consentDetails)
  const points=Array.from(more.querySelectorAll('li'),li=>li.textContent!.trim())
  expect(points).toEqual([copy.pointPassword,copy.pointLimited,copy.pointUpdate,copy.pointRevoke])
  // The disclosure follows the form: the form comes first.
  expect(p.root.querySelector('form')!.compareDocumentPosition(more)&Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
 }finally{p.done()}
})

test('the approval says each thing once: no status badge or callout repeats that it needs approval',async()=>{
 const p=await approval(true,access().setup)
 try{
  expect(p.root.querySelector('[data-executor-status]')).toBeNull()
  expect(p.root.querySelector('.ui-callout')).toBeNull()
  expect(p.root.querySelector('.ui-settings-section-header')).toBeNull()
 }finally{p.done()}
})

test('an App\'s own purpose replaces the generic one',async()=>{
 const purpose='Reclaim empties the recycle bins of every shared folder.'
 const p=await approval(true,access().setup,purpose)
 try{
  const lead=p.root.querySelector('[data-consent-lead]')!.textContent
  expect(lead).toContain(purpose)
  expect(lead).not.toContain(copy.consentPurpose)
 }finally{p.done()}
})

test('anyone else is told why and whom to ask, with no password field',async()=>{
 const p=await approval(false,access().setup)
 try{
  const consent=p.root.querySelector('[data-executor-consent]')!
  expect(consent.querySelector('[data-consent-title]')!.textContent).toBe(copy.consentTitleUser)
  expect(consent.querySelector('[data-consent-lead]')!.textContent).toContain(copy.consentWhy)
  expect(consent.textContent).toContain(copy.consentAsk)
  expect(p.root.querySelector('input[type=password],form')).toBeNull()
  expect(consent.querySelector('[data-consent-points]')).toBeNull()
  expect(consent.classList).toContain('executor-consent-page')
 }finally{p.done()}
})

test('the approve button is a primary action that is never greyed out for an empty password',async()=>{
 const a=access(),p=await approval(true,a.setup)
 try{
  const button=p.root.querySelector<HTMLButtonElement>('button[type=submit]')!
  expect(button.textContent!.trim()).toBe(copy.authorize)
  expect(button.disabled).toBe(false)
  expect(button.getAttribute('data-variant')??button.className).toMatch(/primary/)
  await p.submit()
  expect(a.passwords).toEqual([])
  expect(p.field()!.querySelector('[role=alert]')!.textContent).toBe(copy.passwordRequired)
  await p.type('x')
  expect(p.field()!.querySelector('[role=alert]')).toBeNull()
 }finally{p.done()}
})

for(const [code,key] of [['password_rejected','passwordRejected'],['password_confirm_failed','passwordConfirmFailed']] as const){
 test(`${code} shows at the field and keeps what was typed for another try`,async()=>{
  const a=access(password=>password==='right'?{state:'ready'}:{state:'blocked',code,reason:'dsm_3121',retry:true}),p=await approval(true,a.setup)
  try{
   await p.type('wrong');await p.submit()
   expect(p.field()!.querySelector('[role=alert]')!.textContent).toBe(copy[key])
   expect(p.field()!.getAttribute('data-status')).toBe('error')
   expect(p.root.querySelector('.ui-callout')).toBeNull()
   const input=p.root.querySelector<HTMLInputElement>('#delegate-password')!
   expect(input.value).toBe('wrong')
   expect(input.disabled).toBe(false)
   await p.type('right');await p.submit()
   expect(a.passwords).toEqual(['wrong','right'])
   expect(p.root.textContent).toContain('App content')
  }finally{p.done()}
 })
}

test('an executor with a file scope names file jobs as its purpose',async()=>{
 const ctx={async call<T>(){return {scope_mode:'fixed'} as T}}
 const p=await mount(()=>h(ExecutorSettings,{admin:true,locale:'en',ctx}),access().setup)
 try{expect(p.root.querySelector('[data-consent-lead]')!.textContent).toContain(copy.consentPurposeFiles)}finally{p.done()}
})

test('the notice dialog shows the same approval under one title',async()=>{
 const p=await mount(()=>h(ExecutorNotice,{locale:'en',ctx:{executor:access().setup,user:{isAdmin:true} as never}}),access().setup)
 try{
  Array.from(p.root.querySelectorAll('button')).find(b=>b.textContent!.trim()===copy.noticeApprove)!.click();await settle()
  const dialog=document.querySelector('[role=dialog]')!
  expect(dialog.querySelector('[data-executor-consent]')).not.toBeNull()
  expect(dialog.querySelector('.ui-settings-section-header')).toBeNull()
  expect(dialog.textContent!.split(copy.setupTitle).length-1).toBe(1)
 }finally{p.done()}
})

test('every language says the App is not Synology\'s and that DSM checks the password',()=>{
 for(const [locale,c] of Object.entries(delegateCatalogs)){
  expect(c.consentWhy,locale).toContain('Synology')
  expect(c.consentWhy,locale).toContain('DSM')
  expect(c.pointPassword,locale).toContain('DSM')
  expect(c.passwordNote,locale).toContain('DSM')
 }
})

test('the old approval wording is gone from every language',()=>{
 for(const c of Object.values(delegateCatalogs))for(const key of ['hintPassword','statusNeeds','executorNeeds','scopeNeeds','consentOnce','statusScope'])expect(c).not.toHaveProperty(key)
})


test('WebAPI approval shows each profile purpose and its exact API versions',async()=>{
 const a=access()
 const setup={...a.setup,webapiProfiles:{calendar:{purpose:'Create approved Calendar reminders',raw_max_bytes:26214400,apis:{'SYNO.Cal.Event':{create:[6],list:[6]}}}}}
 const p=await approval(true,setup)
 try {
  const profiles=p.root.querySelector('[data-webapi-profiles]')
  expect(profiles?.textContent).toContain('Create approved Calendar reminders')
  expect(profiles?.textContent).toContain('SYNO.Cal.Event')
  expect(profiles?.textContent).toContain('create (6)')
  expect(profiles?.querySelector('[data-webapi-raw]')?.textContent).toContain('26214400')
 } finally {p.done()}
})
