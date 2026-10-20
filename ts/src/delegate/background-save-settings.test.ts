// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import BackgroundSaveSettings from './BackgroundSaveSettings.vue'
import type {BackgroundSaveFacade,DestinationGrant,DestinationState} from './long-running'
import type {CapabilityStatus} from './executor-setup'
import {ExecutorSetupKey} from './use-executor-setup'
import {saveCopy} from './save-copy'
import {delegateCopy} from './copy'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
const selection={scope:'shared:fgwmrf',root:'/fgwmrf',generation:1}
const grant={id:'g',key:'save',path:'/fgwmrf/Downloads',username:'a',lifetime:'until-revoked' as const}
const active:DestinationState={key:'save',status:'active',selection,destination:grant}
const provider={async shares(){return {entries:[{name:'fgwmrf',path:'/fgwmrf',isDir:true}],total:1,offset:0}},async list(){return {entries:[],total:0,offset:0}},async info(path:string){return {name:'x',path,isDir:true}},async createFolder(){},dispose(){}}
const shares={shares:[{name:'fgwmrf',path:'/fgwmrf',isdir:true},{name:'video',path:'/video',isdir:true}],total:2,offset:0}
interface Options {admin?:boolean;destination?:DestinationState;status?:Record<string,unknown>|(()=>Record<string,unknown>);capability?:CapabilityStatus;authorize?:(password:string,scope?:string)=>Promise<CapabilityStatus>;sdk?:Partial<BackgroundSaveFacade>;props?:Record<string,unknown>;scopeRead?:Promise<unknown>}
async function mount(o:Options={}){
 let selectedGrant:DestinationGrant|undefined
 const calls={authorize:[] as unknown[][],select:[] as unknown[],enable:0,operations:0,disable:0}
 const status=()=>typeof o.status==='function'?o.status():o.status??{state:'ready',disabled:false,scope_mode:'install-selected',selection}
 const call=async<T,>(method:string):Promise<T>=>(method==='bg.browse'?shares:method==='delegate.status'?(o.scopeRead?await o.scopeRead:status()):undefined) as T
 const sdk={
  async status(){const s=status();if(!['ready','disabled','needs-scope-confirmation'].includes(s.state as string))throw {code:'EXECUTOR_UNAVAILABLE'};return {disabled:s.disabled,scopeMode:s.scope_mode,selection:s.selection??null}},
  async destination(key:string){return selectedGrant?{...active,key,destination:selectedGrant}:o.destination??{...active,key}},
  async selectDestination(input:{key:string;path:string}){calls.select.push(input);selectedGrant={...grant,key:input.key,path:input.path};return selectedGrant},
  async revoke(){return {revoked:true}},
  async enable(){calls.enable++;return {disabled:false}},async disable(){calls.disable++;return {disabled:true}},
  async operations(){calls.operations++;return []},
  ...o.sdk,
 } as unknown as BackgroundSaveFacade
 const setup={async status(){return o.capability??{state:'ready'} as CapabilityStatus},authorize:async(password:string,scope?:string)=>{calls.authorize.push([password,scope]);return o.authorize?o.authorize(password,scope):{state:'ready'} as CapabilityStatus},async control(){return {state:'ready'} as CapabilityStatus},call}
 const root=document.createElement('div');document.body.append(root)
 let exposed:{refresh():Promise<void>}|undefined
 const app=createApp({render:()=>h(AppRoot,{},()=>h(BackgroundSaveSettings,{sdk,admin:o.admin??false,locale:'zh-TW',picker:{provider,keys:['save']},ref:(r:unknown)=>{exposed=r as typeof exposed},...o.props}))})
 app.provide(ExecutorSetupKey,setup)
 app.mount(root);await settle()
 const buttons=()=>Array.from(root.querySelectorAll('button')).map(b=>b.textContent!.trim())
 const press=async(label:string)=>{Array.from(document.querySelectorAll('button')).find(b=>b.textContent!.trim()===label)!.click();await settle()}
 const type=async(selector:string,value:string)=>{const input=root.querySelector<HTMLInputElement>(selector)!;input.value=value;input.dispatchEvent(new Event('input'));await settle()}
 const paths=(path:string)=>root.querySelectorAll(`.ui-path-display [data-ui-path-text][title="${path}"]`).length
 const labels=()=>Array.from(root.querySelectorAll('[data-save-row]')).map(r=>r.getAttribute('data-save-row'))
 return {root,calls,buttons,press,type,paths,labels,refresh:()=>exposed!.refresh(),done:()=>{app.unmount();root.remove()}}
}
const s=saveCopy('zh-TW'),d=delegateCopy('zh-TW')

test('a user sees one row: the save folder and a button to choose another',async()=>{
 const p=await mount()
 try{
  expect(p.labels()).toEqual(['folder'])
  expect(p.root.querySelector('[data-save-row="folder"]')!.textContent).toContain(s.folder)
  expect(p.paths('/fgwmrf/Downloads')).toBe(1)
  expect(p.paths('/fgwmrf')).toBe(0)
  expect(p.buttons()).toContain(s.choose)
  expect(p.root.querySelector('input[type=password]')).toBeNull()
  expect(p.root.querySelector('[role=alert],[data-ui-callout]')).toBeNull()
 }finally{p.done()}
})
test('a folder not chosen yet says so beside the button',async()=>{
 const p=await mount({destination:{key:'save',status:'missing',selection,destination:null}})
 try{
  expect(p.root.querySelector('[data-save-row="folder"]')!.textContent).toContain(s.folderNone)
  expect(p.buttons()).toContain(s.choose)
  expect(p.buttons()).not.toContain(s.revoke)
 }finally{p.done()}
})
test('choosing a folder needs no password and shows the new folder',async()=>{
 let chosen:DestinationState={key:'save',status:'missing',selection,destination:null}
 const p=await mount({sdk:{async destination(){return chosen},async selectDestination(input:{key:string;path:string}){chosen={...active,destination:{...grant,path:input.path}};return chosen.destination!}}})
 try{
  await p.press(s.choose)
  document.querySelector('tr[data-row-key="/fgwmrf"]')!.dispatchEvent(new MouseEvent('click',{bubbles:true}));await settle()
  await p.press('確認選取')
  expect(p.paths('/fgwmrf')).toBe(1)
  expect(p.root.querySelector('input[type=password]')).toBeNull()
 }finally{p.done()}
})
test('an administrator sees only the folder row with choose and revoke actions',async()=>{
 const p=await mount({admin:true})
 try{
  expect(p.labels()).toEqual(['folder'])
  expect(p.paths('/fgwmrf/Downloads')).toBe(1)
  expect(p.buttons()).toContain(s.choose)
  expect(p.buttons()).toContain(s.revoke)
  await p.press(s.choose)
  expect(p.root.querySelector('input[type=password]')).not.toBeNull()
  expect(p.root.querySelector('[data-save-row="folder"] [role=combobox]')).not.toBeNull()
  await p.press(s.cancelChange)
  expect(p.root.querySelector('input[type=password]')).toBeNull()
 }finally{p.done()}
})
test('one administrator approval also grants the selected root, without a second picker',async()=>{
 let current={state:'ready',disabled:false,scope_mode:'install-selected',selection} as Record<string,unknown>
 const p=await mount({admin:true,status:()=>current,authorize:async()=>{current={...current,selection:{scope:'shared:video',root:'/video',generation:2}};return {state:'ready'}}})
 try{
  await p.press(s.choose)
  p.root.querySelector<HTMLElement>('[role=combobox]')!.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowDown',bubbles:true,cancelable:true}));await settle()
  Array.from(document.querySelectorAll<HTMLElement>('[role=option]')).find(e=>e.textContent?.trim()==='video')!.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true}));await settle()
  await p.type('input[type=password]','secret')
  p.root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(p.calls.authorize).toEqual([['secret','shared:video']])
  expect(p.calls.select).toEqual([{key:'save',path:'/video'}])
  expect(p.root.querySelector('input[type=password]')).toBeNull()
 }finally{p.done()}
})
test('the first approval chooses the scope in the same step, with one password',async()=>{
 let approved=false
 const p=await mount({admin:true,status:()=>approved?{state:'ready',disabled:false,scope_mode:'install-selected',selection}:{state:'needs-admin-approval',scope_mode:'install-selected',preselection:'shared:fgwmrf',selection:null},capability:{state:'needs-admin-approval'},authorize:async()=>{approved=true;return {state:'ready'}}})
 try{
  expect(p.root.textContent).toContain(s.approveHelp)
  expect(p.buttons()).not.toContain(s.choose)
  await p.type('input[type=password]','secret')
  await p.press(s.approve)
  expect(p.calls.authorize).toEqual([['secret','shared:fgwmrf']])
  expect(p.calls.select).toEqual([{key:'save',path:'/fgwmrf'}])
 }finally{p.done()}
})
test('a user waits for an administrator before choosing a folder',async()=>{
 const p=await mount({status:{state:'needs-admin-approval',scope_mode:'install-selected',selection:null}})
 try{
  expect(p.root.querySelector('[data-executor-consent]')!.textContent).toContain(d.consentAsk)
  expect(p.root.textContent).not.toContain(s.help)
  expect(p.buttons()).not.toContain(s.choose)
  expect(p.root.querySelector('input[type=password]')).toBeNull()
 }finally{p.done()}
})
test('a changed scope asks to choose the folder again',async()=>{
 const p=await mount({destination:{key:'save',status:'reselection-required',selection,destination:null}})
 try{
  expect(p.root.textContent).toContain(s.reselect)
  expect(p.buttons()).toContain(s.choose)
 }finally{p.done()}
})
test('turned off background saves say so; an administrator turns them on there',async()=>{
 const user=await mount({status:{state:'disabled',disabled:true,scope_mode:'install-selected',selection}})
 try{
  expect(user.root.textContent).toContain(s.disabled)
  expect(user.buttons()).not.toContain(s.choose)
  expect(user.buttons()).not.toContain(s.enable)
 }finally{user.done()}
 const admin=await mount({admin:true,status:{state:'disabled',disabled:true,scope_mode:'install-selected',selection}})
 try{await admin.press(s.enable);expect(admin.calls.enable).toBe(1)}finally{admin.done()}
})
test('settings never read operations or offer disable controls',async()=>{
 const p=await mount({admin:true})
 try{
  expect(p.calls.operations).toBe(0)
  expect(p.root.querySelector('[data-save-operations],[data-saves-list],[data-save-danger]')).toBeNull()
  expect(p.calls.disable).toBe(0)
  expect(p.root.textContent).not.toContain('停用背景存檔')
  expect(p.root.textContent).not.toContain('存檔紀錄')
 }finally{p.done()}
})
test('a refused password says so and keeps the form',async()=>{
 const p=await mount({admin:true,authorize:async()=>({state:'blocked',code:'password_rejected',retry:true})})
 try{
  await p.press(s.choose)
  await p.type('input[type=password]','wrong')
  p.root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(p.root.textContent).toContain(d.passwordRejected)
  expect(p.root.querySelector('input[type=password]')).not.toBeNull()
 }finally{p.done()}
})
test('several keys each get a labeled row; a key itself never shows',async()=>{
 const p=await mount({props:{picker:{provider,keys:['save','media'],labels:{save:'檔案',media:'影音'}}}})
 try{
  expect(p.root.textContent).toContain('檔案')
  expect(p.root.textContent).toContain('影音')
  expect(p.root.textContent).not.toContain('media')
 }finally{p.done()}
})
test('the page header refreshes the section',async()=>{
 let reads=0
 const p=await mount({sdk:{async destination(){reads++;return active}}})
 try{const before=reads;await p.refresh();await settle();expect(reads).toBe(before+1)}finally{p.done()}
})
// A scope task can land after the window stops waiting for it: the section
// reads the status again instead of reporting the change as failed.
test('a scope change that lands after the approval returns is read back, not reported as failed',async()=>{
 let current={state:'ready',disabled:false,scope_mode:'install-selected',selection} as Record<string,unknown>
 const p=await mount({admin:true,status:()=>current,authorize:async()=>{current={...current,selection:{scope:'shared:video',root:'/video',generation:2}};return {state:'blocked',code:'selection_failed',reason:'executor_selection_unconfirmed',requested:{scope:'shared:video',generation:1}}}})
 try{
  await p.press(s.choose)
  p.root.querySelector<HTMLElement>('[role=combobox]')!.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowDown',bubbles:true,cancelable:true}));await settle()
  Array.from(document.querySelectorAll<HTMLElement>('[role=option]')).find(e=>e.textContent?.trim()==='video')!.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true}));await settle()
  await p.type('input[type=password]','secret')
  p.root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(p.root.querySelector('[role=alert]')).toBeNull()
  expect(p.paths('/video')).toBe(1)
  expect(p.root.querySelector('input[type=password]')).toBeNull()
 }finally{p.done()}
})

test('the folder action reserves its place while scope permission loads',async()=>{
 let read:(v:unknown)=>void=()=>{}
 const p=await mount({admin:true,scopeRead:new Promise(r=>{read=r})})
 try{
  const choose=()=>Array.from(p.root.querySelectorAll<HTMLButtonElement>('[data-save-row="folder"] button')).find(b=>b.textContent!.trim()===s.choose)!
  expect(choose().disabled).toBe(true)
  read({state:'ready',disabled:false,scope_mode:'install-selected',selection});await settle()
  expect(choose().disabled).toBe(false)
 }finally{p.done()}
})
// the confirm button looked pressable while the approval ran.
test('the confirm button shows it is working while the approval runs',async()=>{
 const p=await mount({admin:true,status:{state:'needs-admin-approval'},capability:{state:'needs-admin-approval'},authorize:()=>new Promise<CapabilityStatus>(()=>{})})
 try{
  await p.type('#save-password','secret')
  p.root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  const submit=p.root.querySelector<HTMLButtonElement>('button[type=submit]')!
  expect(submit.disabled).toBe(true)
  expect(submit.getAttribute('data-loading')).not.toBeNull()
 }finally{p.done()}
})
// while Download Hub was stopped the section showed the approval
// form, and it came back only when the page was reloaded.
test('a stopped package says so with no approval form, and comes back by itself',async()=>{
 vi.useFakeTimers({shouldAdvanceTime:true})
 let stopped=true
 const o:Options={admin:true,capability:{state:'blocked',code:'executor_unreachable',reason:'APP_STOPPED: the executor status request failed.',retry:true}}
 o.sdk={async status(){if(stopped)throw {code:'APP_STOPPED'};return {disabled:false,scopeMode:'install-selected',selection}}}
 const p=await mount(o)
 try{
  expect(p.root.textContent).toContain(d.appStopped)
  expect(p.root.querySelector('input[type=password]')).toBeNull()
  expect(p.root.querySelector('[role=alert]')).toBeNull()
  expect(p.labels()).toEqual([])
  stopped=false;o.capability={state:'ready'}
  await vi.advanceTimersByTimeAsync(2000);await settle()
  expect(p.root.textContent).not.toContain(d.appStopped)
  expect(p.labels()).toEqual(['folder'])
 }finally{p.done();vi.useRealTimers()}
})
test('a status the App reads as unapproved while the window says starting shows no approval form',async()=>{
 const p=await mount({admin:true,status:{state:'blocked'},capability:{state:'blocked',code:'executor_starting',reason:"executor_starting: the executor's start step has not ended."}})
 try{
  expect(p.root.textContent).toContain(d.appStarting)
  expect(p.root.querySelector('input[type=password]')).toBeNull()
 }finally{p.done()}
})
test('anyone else sees a starting package as starting, not as waiting for approval',async()=>{
 const p=await mount({sdk:{async status(){throw {code:'APP_STARTING'}}}})
 try{
  expect(p.root.textContent).toContain(d.appStarting)
  expect(p.root.textContent).not.toContain(s.needs)
  expect(p.root.querySelector('[role=alert]')).toBeNull()
 }finally{p.done()}
})

test('a refused administrator password never creates a destination grant',async()=>{
 const p=await mount({admin:true,authorize:async()=>({state:'blocked',code:'password_rejected',retry:true})})
 try{
  await p.press(s.choose);await p.type('input[type=password]','wrong')
  p.root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(p.calls.select).toEqual([])
  expect(p.root.textContent).toContain(d.passwordRejected)
 }finally{p.done()}
})
test('destination refusal after scope approval stays visible and retryable',async()=>{
 const p=await mount({admin:true,sdk:{async selectDestination(){throw {code:'PERMISSION_DENIED'}}}})
 try{
  await p.press(s.choose);await p.type('input[type=password]','secret')
  p.root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(p.root.querySelector('[role=alert]')?.textContent).toContain(s.permissionSelect)
 }finally{p.done()}
})

test('a ready response without the requested protected scope creates no grant',async()=>{
 const p=await mount({admin:true})
 try{
  await p.press(s.choose)
  p.root.querySelector<HTMLElement>('[role=combobox]')!.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowDown',bubbles:true,cancelable:true}));await settle()
  Array.from(document.querySelectorAll<HTMLElement>('[role=option]')).find(e=>e.textContent?.trim()==='video')!.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true}));await settle()
  await p.type('input[type=password]','secret')
  p.root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(p.calls.authorize).toEqual([['secret','shared:video']])
  expect(p.calls.select).toEqual([])
  expect(p.root.querySelector('[role=alert]')).not.toBeNull()
 }finally{p.done()}
})
// the first approval explains itself as ExecutorApproval does, and a
// refused password stays at its field with what was typed.
test('the first approval explains why before the password and keeps a refused one at its field',async()=>{
 let approved=false
 const p=await mount({admin:true,status:()=>approved?{state:'ready',disabled:false,scope_mode:'install-selected',selection}:{state:'needs-admin-approval',scope_mode:'install-selected',preselection:'shared:fgwmrf',selection:null},capability:{state:'needs-admin-approval'},authorize:async password=>{if(password!=='right')return {state:'blocked',code:'password_rejected',retry:true};approved=true;return {state:'ready'}}})
 try{
  const consent=p.root.querySelector('[data-executor-consent]')!
  expect(consent.querySelector('[data-consent-lead]')!.textContent).toContain(d.consentPurposeFiles)
  // The lead says what the section's help would only repeat.
  expect(p.root.textContent).not.toContain(s.help)
  expect(consent.contains(p.root.querySelector('form'))).toBe(true)
  const button=Array.from(p.root.querySelectorAll<HTMLButtonElement>('button[type=submit]')).find(b=>b.textContent!.trim()===s.approve)!
  expect(button.disabled).toBe(false)
  await p.type('input[type=password]','wrong')
  p.root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  const field=p.root.querySelector('input[type=password]')!.closest('[data-form-item]')!
  expect(field.querySelector('[role=alert]')!.textContent).toBe(d.passwordRejected)
  expect(p.root.querySelector('.ui-callout')).toBeNull()
  expect(p.root.querySelector<HTMLInputElement>('input[type=password]')!.value).toBe('wrong')
 }finally{p.done()}
})
