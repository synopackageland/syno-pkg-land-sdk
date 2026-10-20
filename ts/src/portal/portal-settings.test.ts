// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import PortalSettings from './PortalSettings.vue'
import ExecutorSettings from '../delegate/ExecutorSettings.vue'
import type {PortalAccess,PortalStatus} from '../context'
import type {CapabilityStatus,ExecutorSetup} from '../delegate/executor-setup'
import {ExecutorSetupKey} from '../delegate/use-executor-setup'
import {portalMessages} from './i18n'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await Promise.resolve()}}
const inactive:PortalStatus={state:'inactive',canManage:true,controlPanel:true,defaultAlias:'demo'}
const active:PortalStatus={...inactive,state:'active',alias:'demo',url:'https://nas/demo/'}
afterEach(()=>{vi.useRealTimers();window.history.replaceState({},'', '/')})
// DSM reloads its web server on set, and the reply can arrive late or never.
// the panel stays busy until the change ends, then reads the status again.
test('a change that DSM answers late shows the new status and stays busy until it ends',async()=>{
 vi.useFakeTimers()
 let answer:(s:PortalStatus)=>void=()=>{}
 const status=vi.fn<()=>Promise<PortalStatus>>().mockResolvedValueOnce(inactive).mockResolvedValue(active)
 const portal={messages:new Proxy({},{get:(_,k)=>String(k)}),status,apply:()=>new Promise<PortalStatus>(r=>{answer=r}),disable:async()=>inactive,openControlPanel(){},mountPanel(){}} as unknown as PortalAccess
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal}))})
 try{
  app.mount(root);await settle()
  root.querySelector('form')!.dispatchEvent(new Event('submit'));await settle()
  expect(root.querySelector('[aria-busy=true]')).not.toBeNull()
  await vi.advanceTimersByTimeAsync(5000);await settle()
  expect(root.textContent).toContain('https://nas/demo/')
  expect(root.querySelector('[aria-busy=true]')).not.toBeNull()
  status.mockResolvedValue(inactive)
  answer(active);await settle()
  expect(root.querySelector('[aria-busy=true]')).toBeNull()
  expect(root.textContent).not.toContain('https://nas/demo/')
 }finally{app.unmount();root.remove()}
})
test('a slow change that fails reads the status again and keeps the buttons disabled until then',async()=>{
 vi.useFakeTimers()
 let fail:(e:unknown)=>void=()=>{}
 const registering:PortalStatus={...inactive,entry:'cleanup-required'},after:PortalStatus={...inactive,entry:'unregistered'}
 const status=vi.fn<()=>Promise<PortalStatus>>().mockResolvedValueOnce(after).mockResolvedValue(registering)
 const portal={messages:new Proxy({},{get:(_,k)=>String(k)}),status,apply:async()=>after,disable:async()=>after,
  register:()=>new Promise<PortalStatus>((_,r)=>{fail=r}),unregister:vi.fn(async()=>after),openControlPanel(){},mountPanel(){}} as unknown as PortalAccess
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal}))})
 const button=(label:string)=>Array.from(root.querySelectorAll('button')).find(b=>b.textContent===label)
 try{
  app.mount(root);await settle()
  button('register')!.click();await settle()
  await vi.advanceTimersByTimeAsync(6000);await settle()
  expect(button('unregister')?.disabled??true).toBe(true)
  status.mockResolvedValue(after)
  fail({code:'PORTAL_FAILED'});await settle()
  expect(status).toHaveBeenCalledTimes(3)
  expect(root.textContent).toContain('entryOff')
  expect(root.textContent).toContain('PORTAL_FAILED')
  expect(button('register')!.disabled).toBe(false)
 }finally{app.unmount();root.remove()}
})
// an App below DSM 7.2 has its executor register the entry.
function mountEntry(first:PortalStatus,next:PortalStatus,applied:PortalStatus=first){
 const register=vi.fn(async()=>next),unregister=vi.fn(async()=>next),apply=vi.fn(async(_:string)=>applied)
 const portal={messages:new Proxy({},{get:(_,k)=>String(k)}),status:async()=>first,apply,disable:async()=>first,register,unregister,openControlPanel(){},mountPanel(){}} as unknown as PortalAccess
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal}))})
 app.mount(root)
 const button=(label:string)=>Array.from(root.querySelectorAll('button')).find(b=>b.textContent===label)
 return {root,register,unregister,apply,button,done(){app.unmount();root.remove()}}
}
// recheck: enabling the entry left it inactive until Apply was pressed too.
test('an administrator enables the entry and its default alias in one action',async()=>{
 const v=mountEntry({...inactive,entry:'unregistered'},{...inactive,entry:'registered'},{...active,entry:'registered'})
 try{
  await settle()
  expect(v.root.textContent).toContain('entryOff')
  expect(v.root.querySelector('form')).toBeNull()
  expect(v.button('unregister')).toBeUndefined()
  v.button('register')!.click();await settle()
  expect(v.register).toHaveBeenCalledOnce()
  expect(v.apply).toHaveBeenCalledWith('demo')
  expect(v.root.textContent).toContain('https://nas/demo/')
  expect(v.root.querySelector('form')).not.toBeNull()
  expect(v.button('register')).toBeUndefined()
  expect(v.button('unregister')).toBeDefined()
 }finally{v.done()}
})
test('an alias that DSM refuses after enabling the entry leaves the form to choose another',async()=>{
 const v=mountEntry({...inactive,entry:'unregistered'},{...inactive,entry:'registered'})
 v.apply.mockRejectedValue({code:'portal_alias_in_use'})
 try{
  await settle()
  v.button('register')!.click();await settle()
  expect(v.root.textContent).toContain('portal_alias_in_use')
  expect(v.root.querySelector('form')).not.toBeNull()
  expect(v.button('register')).toBeUndefined()
 }finally{v.done()}
})
test('an administrator removes the entry',async()=>{
 const v=mountEntry({...active,entry:'registered'},{...inactive,entry:'unregistered'})
 try{
  await settle()
  v.button('unregister')!.click();await settle()
  expect(v.unregister).toHaveBeenCalledOnce()
  expect(v.root.textContent).toContain('entryOff')
 }finally{v.done()}
})
// Removing the alias also removes the relative CGI route of the page using it.
test('removal from the alias page requires confirmation and gives desktop recovery instead of enable',async()=>{
 window.history.replaceState({},'', '/demo/')
 const v=mountEntry({...active,url:`${window.location.origin}/demo/`,entry:'registered'},{...inactive,entry:'unregistered'})
 try{
  await settle()
  v.button('unregister')!.click();await settle()
  expect(v.unregister).not.toHaveBeenCalled()
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('removeFromPage')
  Array.from(document.querySelectorAll('button')).find(b=>b.textContent==='cancel')!.click();await settle()
  expect(v.unregister).not.toHaveBeenCalled()
  v.button('unregister')!.click();await settle()
  Array.from(document.querySelector('[role="dialog"]')!.querySelectorAll('button')).find(b=>b.textContent==='unregister')!.click();await settle()
  expect(v.unregister).toHaveBeenCalledOnce()
  expect(v.root.textContent).toContain('removedFromPage')
  expect(v.button('register')).toBeUndefined()
  expect(v.root.querySelector('form')).toBeNull()
 }finally{v.done()}
})
test('a lost removal reply on the alias page gives uncertain desktop recovery without claiming success',async()=>{
 window.history.replaceState({},'', '/demo/')
 const v=mountEntry({...active,url:`${window.location.origin}/demo/`,entry:'registered'},{...inactive,state:'unavailable'})
 v.unregister.mockRejectedValue({code:'PORTAL_FAILED'})
 try{
  await settle();v.button('unregister')!.click();await settle()
  Array.from(document.querySelector('[role="dialog"]')!.querySelectorAll('button')).find(b=>b.textContent==='unregister')!.click();await settle()
  expect(v.root.textContent).toContain('removalUnconfirmed')
  expect(v.root.textContent).not.toContain('removedFromPage')
  expect(v.root.textContent).not.toContain('PORTAL_FAILED')
  expect(v.button('register')).toBeUndefined()
 }finally{v.done()}
})
test('a desktop page or a different alias does not need the page-loss confirmation',async()=>{
 window.history.replaceState({},'', '/demo-other/')
 const v=mountEntry({...active,url:`${window.location.origin}/demo/`,entry:'registered'},{...inactive,entry:'unregistered'})
 try{
  await settle();v.button('unregister')!.click();await settle()
  expect(v.unregister).toHaveBeenCalledOnce()
  expect(document.querySelector('[role="dialog"]')).toBeNull()
  expect(v.button('register')).toBeDefined()
 }finally{v.done()}
})
test('desktop recovery is immediate while removal has no reply and survives a settings remount',async()=>{
 window.history.replaceState({},'', '/demo/')
 const initial={...active,url:`${window.location.origin}/demo/`,entry:'registered'} as const
 const portal:PortalAccess={messages:new Proxy({},{get:(_,k)=>String(k)}),status:vi.fn(async()=>initial),apply:async()=>initial,disable:async()=>inactive,
  unregister:()=>new Promise(()=>{}),openControlPanel(){}}
 const root=document.createElement('div');document.body.append(root)
 let app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal}))})
 try{
  app.mount(root);await settle()
  Array.from(root.querySelectorAll('button')).find(b=>b.textContent==='unregister')!.click();await settle()
  Array.from(document.querySelector('[role="dialog"]')!.querySelectorAll('button')).find(b=>b.textContent==='unregister')!.click();await settle()
  expect(root.textContent).toContain('removalUnconfirmed')
  expect(root.querySelector('a[href="/"]')).not.toBeNull()
  app.unmount()
  vi.mocked(portal.status).mockClear()
  app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal}))});app.mount(root);await settle()
  expect(root.textContent).toContain('removalUnconfirmed')
  expect(portal.status).not.toHaveBeenCalled()
 }finally{app.unmount();root.remove()}
})
test('a failed change asks to remove the entry again',async()=>{
 const v=mountEntry({...inactive,entry:'cleanup-required'},{...inactive,entry:'unregistered'})
 try{
  await settle()
  expect(v.root.textContent).toContain('cleanupRequired')
  expect(v.root.querySelector('form')).toBeNull()
  expect(v.button('register')).toBeUndefined()
  v.button('unregister')!.click();await settle()
  expect(v.unregister).toHaveBeenCalledOnce()
 }finally{v.done()}
})
test('someone else sees no entry controls',async()=>{
 const v=mountEntry({...inactive,canManage:false,entry:'unregistered'},inactive)
 try{
  await settle()
  expect(v.button('register')).toBeUndefined()
  expect(v.button('unregister')).toBeUndefined()
 }finally{v.done()}
})
// approving the executor (or checking its status again) changes the portal status.
test('the panel reads the status again when the executor status changes',async()=>{
 const before:PortalStatus={...inactive,canManage:false},after:PortalStatus={...inactive,entry:'unregistered'}
 const status=vi.fn<()=>Promise<PortalStatus>>().mockResolvedValueOnce(before).mockResolvedValue(after)
 const portal={messages:new Proxy({},{get:(_,k)=>String(k)}),status,apply:async()=>after,disable:async()=>after,openControlPanel(){},mountPanel(){}} as unknown as PortalAccess
 let publish:(s:CapabilityStatus)=>void=()=>{}
 const setup:ExecutorSetup={status:async()=>({state:'ready'}),authorize:async()=>({state:'ready'}),control:async()=>({state:'ready'}),subscribe:l=>{publish=l}}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal}))})
 app.provide(ExecutorSetupKey,setup)
 try{
  app.mount(root);await settle()
  expect(root.textContent).toContain('controlPanel')
  publish({state:'ready'});await settle()
  expect(status).toHaveBeenCalledTimes(2)
  expect(root.textContent).toContain('entryOff')
  expect(root.textContent).not.toContain('controlPanel')
 }finally{app.unmount();root.remove()}
})
test('refreshing the executor settings reads the portal status again',async()=>{
 const before:PortalStatus={...inactive,canManage:false},after:PortalStatus={...inactive,entry:'unregistered'}
 const status=vi.fn<()=>Promise<PortalStatus>>().mockResolvedValueOnce(before).mockResolvedValue(after)
 const portal={messages:new Proxy({},{get:(_,k)=>String(k)}),status,apply:async()=>after,disable:async()=>after,openControlPanel(){},mountPanel(){}} as unknown as PortalAccess
 // Like the DSM window's access: every result reaches every subscriber.
 const listeners:Array<(s:CapabilityStatus)=>void>=[]
 const publish=(s:CapabilityStatus)=>{listeners.forEach(l=>l(s));return s}
 const setup:ExecutorSetup={status:async()=>publish({state:'ready'}),authorize:async()=>publish({state:'ready'}),control:async()=>publish({state:'ready'}),subscribe:l=>{listeners.push(l)}}
 const root=document.createElement('div');document.body.append(root)
 let executor:{refresh():Promise<void>}|undefined
 const app=createApp({render:()=>h(AppRoot,{},()=>[h(PortalSettings,{portal}),h(ExecutorSettings,{admin:true,locale:'en',ref:(r:unknown)=>{executor=r as typeof executor}})])})
 app.provide(ExecutorSetupKey,setup)
 try{
  app.mount(root);await settle()
  status.mockClear()
  await executor!.refresh();await settle()
  expect(status).toHaveBeenCalledOnce()
  expect(root.textContent).toContain('entryOff')
 }finally{app.unmount();root.remove()}
})
// the panel lays out in SettingsRows like the other SDK panels: a label
// column, then the control; the actions share one row under the alias.
const rowOf=(node:Element)=>node.closest('[data-field-control]')!.parentElement!.parentElement!
const rowLabel=(node:Element)=>rowOf(node).firstElementChild!.textContent!.trim()
test('the alias and the link are settings rows, and the panel actions share one row',async()=>{
 const portal={messages:new Proxy({},{get:(_,k)=>String(k)}),status:async()=>({...active,entry:'registered'}),apply:async()=>active,disable:async()=>inactive,openControlPanel(){},mountPanel(){}} as unknown as PortalAccess
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal}))})
 try{
  app.mount(root);await settle()
  expect(rowLabel(root.querySelector('#portal-alias')!)).toBe('alias')
  expect(rowLabel(root.querySelector('a[href="https://nas/demo/"]')!)).toBe('link')
  const button=(label:string)=>Array.from(root.querySelectorAll('button')).find(b=>b.textContent===label)!
  const row=(label:string)=>button(label).parentElement
  expect(row('advanced')).toBe(row('apply'))
  expect(row('unregister')).toBe(row('apply'))
  expect(rowLabel(button('apply'))).toBe('')
  // The actions wrap only at the row's own width, not at a field's.
  expect((button('apply').closest('[data-field-control]') as HTMLElement).style.maxWidth).toBe('')
  expect(root.querySelector('form')!.contains(button('apply'))).toBe(true)
 }finally{app.unmount();root.remove()}
})
// The link text ends in a colon for the CLI's plain panel; a row label has none.
test('the link label drops its colon',async()=>{
 for(const [locale,label] of [['en','Direct link after DSM sign-in'],['zh-TW','DSM 登入後的直接連結']]){
  const portal={messages:portalMessages(locale),status:async()=>({...active,canManage:false}),apply:async()=>active,disable:async()=>inactive,openControlPanel(){}} as unknown as PortalAccess
  const root=document.createElement('div');document.body.append(root)
  const app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal}))})
  try{
   app.mount(root);await settle()
   expect(rowLabel(root.querySelector('a[href="https://nas/demo/"]')!)).toBe(label)
  }finally{app.unmount();root.remove()}
 }
})

// the section appeared seconds after the settings page and moved
// everything under it; it keeps its place with the rows it will show while it loads.
test('the section keeps its place while the status loads, and leaves when it cannot be read',async()=>{
 let answer:(s:PortalStatus)=>void=()=>{},refuse:(e:unknown)=>void=()=>{}
 const portal=(status:()=>Promise<PortalStatus>)=>({messages:new Proxy({},{get:(_,k)=>String(k)}),status,apply:async()=>active,disable:async()=>inactive,openControlPanel(){},mountPanel(){}}) as unknown as PortalAccess
 const mount=async(p:PortalAccess)=>{const root=document.createElement('div');document.body.append(root);const app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal:p}))});app.mount(root);await settle();return {root,done:()=>{app.unmount();root.remove()}}}
 const loading=await mount(portal(()=>new Promise(r=>{answer=r})))
 try{
  expect(loading.root.textContent).toContain('title')
  expect(loading.root.querySelector('[data-placeholder-row]')).not.toBeNull()
  answer(active);await settle()
  expect(loading.root.querySelector('[data-placeholder-row]')).toBeNull()
  expect(loading.root.textContent).toContain('https://nas/demo/')
 }finally{loading.done()}
 const failed=await mount(portal(()=>new Promise((_,r)=>{refuse=r})))
 try{
  refuse({code:'portal_unavailable'});await settle()
  expect(failed.root.textContent).not.toContain('title')
 }finally{failed.done()}
})

// Reclaim's settings moved 100 px when the placeholder's three rows
// became the Control Panel button DSM leaves an administrator whose App it
// does not list. The access has read the status since the window opened, so
// the section starts as the status it last read and reads it again.
test('the section starts as the status the access last read, with no placeholder',async()=>{
 const listed:PortalStatus={state:'unavailable',canManage:false,controlPanel:true,defaultAlias:'demo'}
 const status=vi.fn(()=>new Promise<PortalStatus>(()=>{}))
 const portal={messages:new Proxy({},{get:(_,k)=>String(k)}),status,lastStatus:()=>listed,apply:async()=>active,disable:async()=>inactive,openControlPanel(){}} as unknown as PortalAccess
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(PortalSettings,{portal}))})
 try{
  app.mount(root)
  expect(root.querySelector('[data-placeholder-row]')).toBeNull()
  expect(root.textContent).toContain('controlPanel')
  expect(status).toHaveBeenCalled()
 }finally{app.unmount();root.remove()}
})
