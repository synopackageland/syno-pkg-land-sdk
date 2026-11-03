// @vitest-environment jsdom
// The folder picker keeps its layout while it loads, says no more than it
// must, and loads from what it already listed. test/browser/picker-layout.mjs
// measures the same layout in a real browser.
import {readFileSync} from 'node:fs'
import {join} from 'node:path'
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick,ref} from 'vue'
import {AppRoot,ConfigProvider,type UiLocale} from '@synopackageland/ui'
import NasPicker from './NasPicker.vue'
import type {NasEntry,NasProvider} from './contract'
import {nasCatalogs,nasCopy} from './locale'
import {saveCopy} from '../delegate/save-copy'
import {createNasProvider} from './provider'

globalThis.CSS??={escape:(value:string)=>value.replace(/[^\w-]/g,'\\$&')} as typeof CSS
async function settle(){for(let i=0;i<30;i++){await Promise.resolve();await nextTick()}await new Promise(r=>setTimeout(r,20))}
const cleanup:Array<()=>void>=[]
afterEach(()=>{cleanup.splice(0).forEach(fn=>fn())})

const dir=(path:string):NasEntry=>({name:path.split('/').pop()!,path,isDir:true})
const listing:Record<string,NasEntry[]>={'':[dir('/projects'),dir('/media')],'/projects':[dir('/projects/design'),{name:'a.txt',path:'/projects/a.txt',isDir:false,bytes:2}],'/projects/design':[],'/media':[]}
// A provider whose answers wait until release() while hold is on.
function gated(){
 const waiting:Array<()=>void>=[],calls:string[]=[]
 let hold=false
 const answer=async(key:string,offset:number)=>{calls.push(key);if(hold)await new Promise<void>(r=>waiting.push(r));const entries=listing[key]??[];return {entries:entries.slice(offset),total:entries.length,offset}}
 const provider:NasProvider={shares:offset=>answer('',offset),list:(path,o)=>answer(path,o.offset??0),info:async p=>dir(p),createFolder:async()=>{},dispose(){}}
 return {provider,calls,hold(on:boolean){hold=on;if(!on)waiting.splice(0).forEach(r=>r())},get pending(){return waiting.length}}
}
function mount(provider:NasProvider,extra:Record<string,unknown>={},locale:UiLocale='en'){
 const root=document.createElement('div');document.body.append(root)
 const open=ref(true)
 const app=createApp({render:()=>h(ConfigProvider,{locale},()=>h(AppRoot,{},()=>h(NasPicker,{provider,locale,mode:'folder',open:open.value,'onUpdate:open':(v:boolean)=>{open.value=v},...extra})))})
 app.config.idPrefix='picker-layout';app.mount(root)
 cleanup.push(()=>{app.unmount();root.remove()})
 return {root,open}
}
const dialog=()=>document.querySelector<HTMLElement>('[role=dialog]')!
const row=(path:string)=>dialog().querySelector<HTMLElement>(`tr[data-row-key="${path}"]`)
const click=(node:HTMLElement)=>node.dispatchEvent(new MouseEvent('click',{bubbles:true}))
const dblclick=(node:HTMLElement)=>{click(node);node.dispatchEvent(new MouseEvent('dblclick',{bubbles:true}))}
// The browser's blocks and their order: loading must not add or remove one.
function blocks(){
 const browser=dialog().querySelector<HTMLElement>('.file-browser')!
 return Array.from(browser.children).map(e=>e.className.replace(/\s+/g,' ').trim())
}
// Text a user reads: overlays carry an accessible name, not a visible row.
function visibleText(node:HTMLElement){return node.textContent!.replace(/\s+/g,' ').trim()}

test('loading inserts no text row: the blocks stay and one spinner overlays the list',async()=>{
 const g=gated();g.hold(true);mount(g.provider);await settle()
 expect(g.pending).toBe(1)
 const loading=blocks()
 expect(visibleText(dialog())).not.toContain(nasCopy('en').loading)
 expect(dialog().querySelectorAll('[role=dialog] [role=status]:not(:empty), [role=dialog] [data-loading-overlay]').length).toBeGreaterThan(0)
 expect(dialog().querySelectorAll('.file-content [data-loading-overlay]')).toHaveLength(1)
 g.hold(false);await settle()
 expect(row('/projects')).not.toBeNull()
 expect(blocks()).toEqual(loading)
 g.hold(true);dblclick(row('/projects')!);await settle()
 expect(blocks()).toEqual(loading)
 expect(visibleText(dialog())).not.toContain(nasCopy('en').loading)
 g.hold(false);await settle()
 expect(row('/projects/design')).not.toBeNull()
 expect(blocks()).toEqual(loading)
})

test('the picker has no default subtitle; an App description still shows',async()=>{
 const g=gated();mount(g.provider);await settle()
 expect(dialog().textContent).not.toContain(nasCopy('en').ownPermissions)
 const described=dialog().getAttribute('aria-describedby')
 expect(described?document.getElementById(described)?.textContent??'':'').toBe('')
 cleanup.splice(0).forEach(fn=>fn())
 mount(gated().provider,{description:'Pick where the export goes'});await settle()
 expect(dialog().textContent).toContain('Pick where the export goes')
})

test('the lasting grant is a footer info tip that describes Confirm, not a paragraph',async()=>{
 const g=gated(),text=saveCopy('en').selection
 mount(g.provider,{destination:{key:'save',selectDestination:vi.fn()}});await settle()
 expect(dialog().querySelector('.save-consent')).toBeNull()
 const body=dialog().querySelector<HTMLElement>('.nas-picker-content')!
 expect(body.textContent).not.toContain(text)
 const footer=dialog().querySelector('footer')!
 const confirm=Array.from(footer.querySelectorAll('button')).find(b=>b.textContent?.trim()===nasCopy('en').confirm)!
 const described=(confirm.getAttribute('aria-describedby')??'').split(' ').map((id:string)=>document.getElementById(id)?.textContent).join(' ')
 expect(described).toContain(text)
 expect(footer.querySelector(`[aria-label="${text}"]`)).not.toBeNull()
})

test('the selected path sits in the footer and names itself in full when cut',async()=>{
 const g=gated();mount(g.provider);await settle()
 dblclick(row('/projects')!);await settle()
 const status=dialog().querySelector<HTMLElement>('footer .nas-picker-selection')!
 expect(status.textContent).toContain('/projects')
 expect(status.getAttribute('title')).toBe('/projects')
 expect(dialog().querySelector('.file-browser .file-selection')).toBeNull()
})

test('reopening shows the folders it listed at once and checks them again',async()=>{
 const g=gated(),{open}=mount(g.provider);await settle()
 open.value=false;await settle()
 g.hold(true);open.value=true;await settle()
 expect(g.pending).toBe(1)
 expect(row('/projects')).not.toBeNull()
 expect(dialog().querySelector('.file-content [data-loading-overlay]')).toBeNull()
 g.hold(false);await settle()
 expect(g.calls.filter(k=>k==='')).toHaveLength(2)
})

test('a highlighted folder lists its children ahead, so opening it shows them at once',async()=>{
 const g=gated();mount(g.provider);await settle()
 dblclick(row('/projects')!);await settle()
 click(row('/projects/design')!);await settle()
 expect(g.calls).toContain('/projects/design')
 g.hold(true);dblclick(row('/projects/design')!);await settle()
 expect(dialog().querySelector('.file-empty')).not.toBeNull()
 g.hold(false);await settle()
})

test('opening a folder while another loads replaces that load instead of being ignored',async()=>{
 const g=gated();mount(g.provider);await settle()
 g.hold(true);dblclick(row('/projects')!);await settle()
 expect(g.pending).toBe(1)
 const back=Array.from(dialog().querySelectorAll('button')).find(b=>b.getAttribute('aria-label')===nasCopy('en').back)!
 expect(back.disabled).toBe(false)
 g.hold(false);await settle()
})

test('reads that start together share one admission check, and a refused App still asks DSM nothing',async()=>{
 const fetch=vi.fn(async()=>new Response(JSON.stringify({success:true,data:{shares:[],files:[],offset:0,total:0}})))
 vi.stubGlobal('fetch',fetch);cleanup.push(()=>vi.unstubAllGlobals())
 let admit!:()=>void
 const call=vi.fn(async()=>{await new Promise<void>(r=>admit=r);return {}})
 const provider=createNasProvider({call:call as any,files:{token:()=>'t'} as any})
 const pages=Promise.all([provider.shares(0,new AbortController().signal),provider.list('/projects',{},new AbortController().signal)])
 await settle()
 expect([call.mock.calls.length,fetch.mock.calls.length]).toEqual([1,0])
 admit();await pages
 expect(fetch).toHaveBeenCalledTimes(2)
 call.mockImplementation(async()=>{throw {code:'app_privilege_denied'}})
 await expect(provider.shares(0,new AbortController().signal)).rejects.toEqual({code:'app_privilege_denied'})
 expect([call.mock.calls.length,fetch.mock.calls.length]).toEqual([2,2])
 provider.dispose()
})

test('picker text ends without a closing period in all nine languages',async()=>{
 expect(Object.keys(nasCatalogs).sort()).toHaveLength(9)
 for(const locale of Object.keys(nasCatalogs) as UiLocale[]){
  const g=gated();mount(g.provider,{destination:{key:'save',selectDestination:vi.fn()}},locale);await settle()
  dblclick(row('/projects')!);await settle();dblclick(row('/projects/design')!);await settle()
  const texts=Array.from(dialog().querySelectorAll('*')).flatMap(e=>Array.from(e.childNodes).filter(n=>n.nodeType===3).map(n=>n.textContent!.trim())).filter(Boolean)
  const labels=Array.from(dialog().querySelectorAll('[aria-label],[title]')).flatMap(e=>[e.getAttribute('aria-label'),e.getAttribute('title')]).filter(Boolean) as string[]
  expect([...texts,...labels].filter(t=>/[.。．]$/.test(t)&&!/\.\.\.$/.test(t)),locale).toEqual([])
  cleanup.splice(0).forEach(fn=>fn())
 }
})

test('the picker styles never force a scrollbar or a minimum wider than the dialog',()=>{
 for(const file of ['NasPicker.vue','../files/FileBrowser.vue']){
  const css=readFileSync(join(__dirname,file),'utf8').split('<style')[1]??''
  expect(css,file).not.toMatch(/overflow(-[xy])?:\s*scroll/)
  expect(css,file).not.toMatch(/min-width:\s*[1-9]\d{2,}px/)
 }
})
