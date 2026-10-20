// @vitest-environment jsdom
// @vitest-environment-options {"url":"https://nas.test"}
// panels lay out as SettingsSection blocks from ui 0.9.3: the help is the
// section's description, a first load shows the rows it will show (the regression test,
// ), and a section's closing actions sit in its footer.
import {afterEach,expect,test} from 'vitest'
import {createApp,h,nextTick,type Component} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import BackgroundSavePanel from './delegate/BackgroundSavePanel.vue'
import ExecutorSettings from './delegate/ExecutorSettings.vue'
import DelegatePanel from './delegate/DelegatePanel.vue'
import BackgroundPanel from './background/BackgroundPanel.vue'
import FilesPage from './files/FilesPage.vue'
import LoginPage from './identity/LoginPage.vue'
import PortalSettings from './portal/PortalSettings.vue'
import {createSession} from './identity/session'
import {identityCopy} from './identity/i18n'
import {ExecutorSetupKey} from './delegate/use-executor-setup'
import type {BackgroundSaveFacade} from './delegate/long-running'
import type {Context,PortalAccess,PortalStatus} from './context'
import {saveCopy} from './delegate/save-copy'
import {delegateCopy} from './delegate/copy'
import {backgroundCopy} from './background/i18n'
import {nasCopy} from './nas/locale'
const settle=async()=>{for(let i=0;i<10;i++){await nextTick();await new Promise(r=>setTimeout(r))}}
const never=()=>new Promise<never>(()=>{})
const cleanup:Array<()=>void>=[]
afterEach(()=>{cleanup.splice(0).forEach(fn=>fn())})
async function mount(component:Component,props:Record<string,unknown>,provide?:[symbol|string,unknown]){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(component,props))})
 if(provide)app.provide(provide[0] as symbol,provide[1])
 app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 await settle()
 return root
}
// SettingsSection draws its description above the content, capped for reading.
const description=(root:Element)=>root.querySelector('.ui-settings-section-body > p.max-w-\\[72ch\\]')?.textContent?.trim()
const skeleton=(root:Element)=>root.querySelector('[aria-busy="true"] [data-ui-skeleton-line]')
// The footer is the section body's last child, after the content.
const inFooter=(root:Element,node:Element)=>{const body=node.closest('.ui-settings-section-body')!;return body.lastElementChild!==node&&body.lastElementChild!.contains(node)&&!body.lastElementChild!.matches('p')}
const save=(over:Partial<Record<keyof BackgroundSaveFacade,unknown>>={})=>({async status(){return {disabled:false}},async destination(key:string){return {key,status:'missing',selection:null,destination:null}},async operations(){return []},...over}) as unknown as BackgroundSaveFacade
const setup=(status:()=>Promise<unknown>)=>[ExecutorSetupKey,{status,async authorize(){return {state:'ready'}},async control(){return {state:'ready'}}}] as [symbol,unknown]

test('BackgroundSavePanel: help and the single folder row keep their place',async()=>{
 const copy=saveCopy('en')
 const loading=await mount(BackgroundSavePanel,{sdk:save({status:never}),admin:true,locale:'en',picker:{provider:{dispose(){}},keys:['save']}})
 expect(description(loading)).toBe(copy.help)
 expect(loading.querySelector('[data-placeholder-row]')).not.toBeNull()
 const ready=await mount(BackgroundSavePanel,{sdk:save(),admin:true,locale:'en'})
 expect(ready.querySelector('[data-placeholder-row]')).toBeNull()
 expect(ready.querySelector('[data-save-danger],[data-save-operations]')).toBeNull()
})
// CLS: three skeleton lines gave way to one status row, moving
// Reclaim's settings below them; a first load shows that row instead.
test('ExecutorSettings: help is the description and a first load the status row it will show',async()=>{
 const copy=delegateCopy('en')
 const loading=await mount(ExecutorSettings,{admin:true,locale:'en'},setup(never))
 expect(description(loading)).toBe(copy.setupHelp)
 expect(skeleton(loading)).toBeNull()
 expect(loading.querySelector('[aria-busy=true]')).not.toBeNull()
 const labels=(root:Element)=>Array.from(root.querySelectorAll('[data-settings-label]'),n=>n.textContent)
 expect(loading.querySelector('[data-placeholder-row]')).not.toBeNull()
 const ready=await mount(ExecutorSettings,{admin:true,locale:'en'},setup(async()=>({state:'ready'})))
 expect(ready.querySelector('[data-placeholder-row]')).toBeNull()
 expect(labels(loading)).toEqual(labels(ready))
})
// DelegatePanel and BackgroundPanel passed loading to SettingsSection,
// whose three skeleton lines gave way to the panel's rows. A first load shows
// those rows, waiting, instead.
test('DelegatePanel: help is the description and a first load the rows it will show',async()=>{
 const copy=delegateCopy('en')
 const loading=await mount(DelegatePanel,{ctx:{call:never,locale:'en'}})
 expect(description(loading)).toBe(copy.help)
 expect(skeleton(loading)).toBeNull()
 expect(loading.querySelector('[aria-busy=true]')).not.toBeNull()
 expect(loading.querySelectorAll('[data-placeholder-row]')).toHaveLength(2)
 expect(loading.textContent).not.toContain(copy.noJobs)
 const call=async(method:string)=>method==='delegate.status'?{state:'ready'}:method==='delegate.list'?{delegations:[]}:{jobs:[]}
 const ready=await mount(DelegatePanel,{ctx:{call,locale:'en'}})
 expect(ready.querySelector('[data-placeholder-row]')).toBeNull()
 const inputs=(root:Element)=>Array.from(root.querySelectorAll('input'),input=>input.id)
 expect(inputs(loading)).toEqual(inputs(ready))
})
test('BackgroundPanel: help is the description and a first load the rows it will show',async()=>{
 const copy=backgroundCopy('en')
 const loading=await mount(BackgroundPanel,{ctx:{call:never,locale:'en'}})
 expect(description(loading)).toBe(copy.help)
 expect(skeleton(loading)).toBeNull()
 expect(loading.querySelector('[aria-busy=true]')).not.toBeNull()
 expect(loading.querySelector('[data-placeholder-row]')).not.toBeNull()
 const status={admin:false,revision:1,worker:{username:'worker',alive:true},limits:{maxInputBytes:1024,maxQueued:1,maxOwnerPending:1,maxOwnerBytes:1024,jobTimeoutSeconds:60}}
 const call=async(method:string)=>method==='bg.status'?status:method==='bg.jobs.list'?{jobs:[]}:{schedules:[]}
 const ready=await mount(BackgroundPanel,{ctx:{call,locale:'en'}})
 expect(ready.querySelector('[data-placeholder-row]')).toBeNull()
 // The same paragraphs and buttons, in the same order, before and after.
 const shape=(root:Element)=>Array.from(root.querySelector('.ui-settings-section-body')!.querySelectorAll(':scope > p, :scope > div'),el=>el.tagName+(el.matches('.integration-actions')?'.actions':''))
 expect(shape(loading)).toEqual(shape(ready).slice(0,shape(loading).length))
 const buttons=(root:Element)=>Array.from(root.querySelectorAll('.integration-actions button'),b=>b.textContent?.trim()).slice(0,2)
 expect(buttons(loading)).toEqual(buttons(ready))
})
test('no SDK section draws the section skeleton: a first load shows its rows',async()=>{
 const {readFileSync,readdirSync}=await import('node:fs')
 const {resolve}=await import('node:path')
 const vue=(dir:string):string[]=>readdirSync(dir,{withFileTypes:true}).flatMap(e=>e.isDirectory()?vue(resolve(dir,e.name)):e.name.endsWith('.vue')?[resolve(dir,e.name)]:[])
 expect(vue(__dirname).filter(file=>/<SettingsSection\b[^>]*\s:?loading\b/.test(readFileSync(file,'utf8')))).toEqual([])
})
test('FilesPage: the permission note is the description',async()=>{
 const call=async(method:string)=>{if(method==='nas.session.status')return {};if(method==='nas.files.shares')return {shares:[],offset:0,total:0};throw {code:'unknown_method'}}
 const root=await mount(FilesPage,{ctx:{call,locale:'en',reopen:()=>()=>{}} as unknown as Context})
 expect(description(root)).toBe(nasCopy('en').ownPermissions)
})
test('LoginPage: the introduction is the description',async()=>{
 const call=async(method:string)=>{if(method==='identity.status')return {enabled:true};throw {code:'session_required'}}
 const session=createSession({locale:'en',call,reopen(){return ()=>{}}} as unknown as Context)
 cleanup.push(()=>session.dispose())
 const root=await mount(LoginPage,{session,locale:'en'})
 expect(description(root)).toBe(identityCopy('en').intro)
})
test('PortalSettings: registering the entry, or opening Control Panel, is the footer',async()=>{
 const base:PortalStatus={state:'inactive',canManage:true,controlPanel:true,defaultAlias:'demo'}
 const portal=(status:PortalStatus)=>({messages:new Proxy({},{get:(_,k)=>String(k)}),status:async()=>status,apply:never,disable:never,register:never,unregister:never,openControlPanel(){}}) as unknown as PortalAccess
 const button=(root:Element,label:string)=>Array.from(root.querySelectorAll('button')).find(b=>b.textContent?.trim()===label)!
 const entry=await mount(PortalSettings,{portal:portal({...base,entry:'unregistered'})})
 expect(inFooter(entry,button(entry,'register'))).toBe(true)
 const viewer=await mount(PortalSettings,{portal:portal({...base,canManage:false})})
 expect(inFooter(viewer,button(viewer,'controlPanel'))).toBe(true)
})
test('PortalSettings: the state note is the description',async()=>{
 const base:PortalStatus={state:'inactive',canManage:true,controlPanel:true,defaultAlias:'demo'}
 const portal=(status:PortalStatus)=>({messages:new Proxy({},{get:(_,k)=>String(k)}),status:async()=>status,apply:never,disable:never,register:never,unregister:never,openControlPanel(){}}) as unknown as PortalAccess
 const cases:Array<[PortalStatus,string|undefined]>=[
  [base,'inactive'],
  [{...base,entry:'unregistered'},'entryOff'],
  [{...base,entry:'cleanup-required'},'cleanupRequired'],
  [{...base,canManage:false,controlPanel:false},'askAdmin'],
  [{...base,state:'active',alias:'demo',url:'https://nas/demo/'},undefined],
 ]
 for(const [status,note] of cases)expect(description(await mount(PortalSettings,{portal:portal(status)})),JSON.stringify(status)).toBe(note)
})
// SDK panels share the page's label column with the App's own rows, and
// their sub-groups are nested sections, not hand-drawn headings.
const fixedWidth=(root:Element)=>Array.from(root.querySelectorAll<HTMLElement>('[style]')).filter(el=>/10rem/.test(el.getAttribute('style')!))
const subSection=(root:Element,title:string)=>Array.from(root.querySelectorAll('.ui-settings-section-body section')).find(s=>s.textContent?.trim().startsWith(title))
test('BackgroundSavePanel and ExecutorSettings keep the shared label column',async()=>{
 const ready=async()=>({state:'ready',scope:{selection:{root:'/media'}}})
 const panel=await mount(BackgroundSavePanel,{sdk:save(),admin:true,locale:'en',picker:{provider:{dispose(){}},keys:['save']}})
 expect(panel.querySelector('[data-save-row="folder"]')).not.toBeNull()
 expect(fixedWidth(panel)).toEqual([])
 const executor=await mount(ExecutorSettings,{admin:true,locale:'en'},setup(ready))
 expect(executor.querySelector('[data-executor-status]')).not.toBeNull()
 expect(fixedWidth(executor)).toEqual([])
})
test('BackgroundSavePanel: the App may rename or drop the settings title',async()=>{
 const copy=saveCopy('en')
 const named=await mount(BackgroundSavePanel,{sdk:save(),admin:true,locale:'en'})
 expect(named.querySelector('section')?.textContent).toContain(copy.title)
 expect(named.querySelector('h3')).toBeNull()
 expect(named.querySelector('[data-save-operations]')).toBeNull()
 const renamed=await mount(BackgroundSavePanel,{sdk:save(),admin:true,locale:'en',title:'Destinations'})
 expect(renamed.textContent).toContain('Destinations')
 expect(renamed.textContent).not.toContain(copy.title)
})
test('no SDK component draws its own heading; sub-groups are nested sections',async()=>{
 const {readFileSync,readdirSync}=await import('node:fs')
 const {resolve}=await import('node:path')
 const vue=(dir:string):string[]=>readdirSync(dir,{withFileTypes:true}).flatMap(e=>e.isDirectory()?vue(resolve(dir,e.name)):e.name.endsWith('.vue')?[resolve(dir,e.name)]:[])
 expect(vue(__dirname).filter(file=>/<h[1-6][\s>]/.test(readFileSync(file,'utf8')))).toEqual([])
})
test('a status summary that ends its section leaves the gap to the next section to the page',async()=>{
 const {readFileSync}=await import('node:fs')
 for(const [file,rule] of [['delegate/ExecutorSettings.vue','.executor-summary']])
  expect(readFileSync(`${__dirname}/${file}`,'utf8'),file).toContain(`${rule}:not(:last-child) {margin-bottom`)
})
// (Reclaim): ExecutorSettings laid its status out as KeyValueRow and
// AuthorizationSettings as SettingsRow, so at a narrow width one stacked and
// the other did not. Settings sections lay their rows out as SettingsRow.
test('ExecutorSettings lays its status out in settings rows',async()=>{
 const copy=delegateCopy('en')
 const root=await mount(ExecutorSettings,{admin:true,locale:'en'},setup(async()=>({state:'blocked',reason:'executor-not-ready'})))
 const labels=Array.from(root.querySelectorAll('[data-settings-label]'),label=>label.textContent?.trim())
 expect(labels).toEqual(expect.arrayContaining([copy.status,copy.details]))
 expect(root.querySelector('[data-executor-status]')!.closest('[data-settings-label] + *')).not.toBeNull()
})
