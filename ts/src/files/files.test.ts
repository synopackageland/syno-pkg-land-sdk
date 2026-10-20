// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick,ref} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import FileBrowser from './FileBrowser.vue'
import NasPicker from '../nas/NasPicker.vue'
import {createNasProvider} from '../nas/provider'
import {nasCopy} from '../nas/locale'

// jsdom has no CSS.escape; UI's grid uses it to focus the row an arrow key selects.
globalThis.CSS??={escape:(value:string)=>value.replace(/[^\w-]/g,'\\$&')} as typeof CSS
async function settle(){for(let i=0;i<30;i++){await Promise.resolve();await nextTick()}await new Promise(r=>setTimeout(r,20))}
const cleanup:Array<()=>void>=[]
afterEach(()=>{cleanup.splice(0).forEach(fn=>fn())})
const folder=(path:string)=>({path,name:path.split('/').at(-1)!,isdir:true})
const file=(path:string)=>({path,name:path.split('/').at(-1)!,isdir:false,additional:{size:2}})
function mount(component:any,call:any,extra:Record<string,unknown>={}){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(component,{provider:createNasProvider({call}),locale:'en',...extra}))})
 app.mount(root);cleanup.push(()=>{app.unmount();root.remove()});return root
}
function button(root:HTMLElement,label:string){const node=Array.from(root.querySelectorAll('button')).find(n=>n.textContent?.trim()===label||n.getAttribute('aria-label')===label);expect(node,label).toBeTruthy();return node!}
const crumbs=(root:HTMLElement)=>Array.from(root.querySelectorAll<HTMLElement>(`nav[aria-label="${nasCopy('en').location}"] ol button`))
// UI 0.9.1 renders the current level as text, not a button.
const current=(root:HTMLElement)=>root.querySelector<HTMLElement>(`nav[aria-label="${nasCopy('en').location}"] ol [aria-current="page"]`)
function row(root:HTMLElement,path:string){const node=root.querySelector<HTMLElement>(`tr[data-row-key="${path}"]`);expect(node,path).toBeTruthy();return node!}
const click=(node:HTMLElement)=>node.dispatchEvent(new MouseEvent('click',{bubbles:true}))
const dblclick=(node:HTMLElement)=>{click(node);click(node);node.dispatchEvent(new MouseEvent('dblclick',{bubbles:true}))}
function fixture(){
 return vi.fn(async(method:string,params:any)=>{
  if(method==='nas.files.shares')return {shares:[folder('/homes'),folder('/shared')],offset:0,total:2}
  if(method==='nas.files.list')return {files:params.path==='/homes'?[folder('/homes/sub'),file('/homes/a.txt')]:[],offset:params.offset??0,total:params.path==='/homes'?2:0}
  throw {code:'unknown_method'}
 })
}
test('UI 0.11 keeps every server page visible and its filler cell does not change path selection',async()=>{
 const call=vi.fn(async(_method:string,params:any)=>({
  shares:Array.from({length:30},(_,i)=>folder(`/share-${(params.offset??0)+i}`)),
  offset:params.offset??0,total:60,
 }))
 const selected=vi.fn(),root=mount(FileBrowser,call,{mode:'folder',onSelected:selected});await settle()
 expect(root.querySelectorAll('tbody tr[data-row-key]')).toHaveLength(30)
 const last=row(root,'/share-29')
 expect(last.querySelector('[data-grid-filler]')).toBeTruthy()
 click(last.querySelector<HTMLElement>('[data-grid-filler]')!);await settle()
 expect(selected).toHaveBeenLastCalledWith('/share-29')
 button(root,nasCopy('en').loadMore).click();await settle()
 expect(call.mock.calls.map(([,params])=>params.offset)).toEqual([0,30])
 expect(root.querySelectorAll('tbody tr[data-row-key]')).toHaveLength(60)
 click(row(root,'/share-59'));await settle()
 expect(selected).toHaveBeenLastCalledWith('/share-59')
})
test('tree loads roots only and loads children on first expansion, including DSM-returned homes',async()=>{
 const call=fixture(),root=mount(FileBrowser,call);await settle()
 expect(call.mock.calls.map(([m])=>m)).toEqual(['nas.files.shares'])
 expect(root.textContent).toContain('homes')
 button(root,'Expand homes').click();await settle()
 expect(call.mock.calls.filter(([m])=>m==='nas.files.list')).toHaveLength(1)
 expect(root.textContent).toContain('sub')
 button(root,'Collapse homes').click();await settle();button(root,'Expand homes').click();await settle()
 expect(call.mock.calls.filter(([m])=>m==='nas.files.list')).toHaveLength(1)
})
test.each(['folder','file'] as const)('picker returns a DSM path in %s mode without creating grants',async(mode)=>{
 const call=fixture(),selected=vi.fn(),root=mount(NasPicker,call,{open:true,mode,onSelected:selected});await settle()
 dblclick(row(root,'/homes'));await settle()
 if(mode==='file'){click(row(root,'/homes/a.txt'));await settle()}
 button(root,nasCopy('en').confirm).click();await settle()
 expect(selected).toHaveBeenCalledWith(mode==='file'?'/homes/a.txt':'/homes')
 expect(call.mock.calls.every(([m])=>!m.includes('grants'))).toBe(true)
})
test('a click selects a folder row, a double click opens it, and the table has no buttons',async()=>{
 const call=fixture(),selected=vi.fn(),root=mount(NasPicker,call,{open:true,mode:'folder',onSelected:selected});await settle()
 dblclick(row(root,'/homes'));await settle()
 expect(root.querySelectorAll('tbody button')).toHaveLength(0)
 click(row(root,'/homes/sub'));await settle()
 expect(row(root,'/homes/sub').className).toContain('surface-selected')
 button(root,nasCopy('en').confirm).click();await settle()
 expect(selected).toHaveBeenCalledWith('/homes/sub')
})
test('file mode never selects a folder row',async()=>{
 const root=mount(NasPicker,fixture(),{open:true,mode:'file'});await settle()
 click(row(root,'/homes'));await settle()
 expect(button(root,nasCopy('en').confirm).disabled).toBe(true)
})
test('folder mode keeps the open folder chosen while a file row is highlighted',async()=>{
 const selected=vi.fn(),root=mount(NasPicker,fixture(),{open:true,mode:'folder',onSelected:selected});await settle()
 dblclick(row(root,'/homes'));await settle();click(row(root,'/homes/a.txt'));await settle()
 button(root,nasCopy('en').confirm).click();await settle()
 expect(selected).toHaveBeenCalledWith('/homes')
})
test('rows are one tab stop: Enter opens a folder and the arrows move the selection',async()=>{
 const call=fixture(),selected=vi.fn(),root=mount(NasPicker,call,{open:true,mode:'file',onSelected:selected});await settle()
 const key=(path:string,k:string)=>row(root,path).dispatchEvent(new KeyboardEvent('keydown',{key:k,bubbles:true}))
 key('/homes','Enter');await settle()
 expect(Array.from(root.querySelectorAll<HTMLElement>('tbody [tabindex="0"]')).map(n=>n.dataset.rowKey)).toEqual(['/homes/sub'])
 key('/homes/sub','ArrowDown');await settle()
 expect(row(root,'/homes/a.txt').getAttribute('aria-selected')).toBe('true')
 expect(document.activeElement).toBe(row(root,'/homes/a.txt'))
 button(root,nasCopy('en').confirm).click();await settle()
 expect(selected).toHaveBeenCalledWith('/homes/a.txt')
})
test('the path is a breadcrumb whose last level is the open folder, and back and refresh are icon buttons',async()=>{
 const call=fixture(),root=mount(FileBrowser,call);await settle()
 dblclick(row(root,'/homes'));await settle()
 const trail=root.querySelector<HTMLElement>(`nav[aria-label="${nasCopy('en').location}"] ol`)!
 expect(Array.from(trail.children).map(li=>li.textContent?.trim())).toEqual([nasCopy('en').roots,'homes'])
 const crumbs=trail.querySelectorAll('button'),lists=()=>call.mock.calls.filter(([m])=>m==='nas.files.list').length,before=lists()
 expect([crumbs.length,current(root)?.tagName,current(root)?.textContent?.trim()]).toEqual([1,'SPAN','homes'])
 current(root)!.click();await settle();expect(lists()).toBe(before)
 for(const label of [nasCopy('en').back,nasCopy('en').refresh])expect(button(root,label).textContent?.trim()).toBe('')
 crumbs[0].click();await settle()
 expect(call.mock.calls.filter(([m])=>m==='nas.files.shares')).toHaveLength(2)
})
// UI's FileTypeIcon colours folders with --ui-file-folder and files with --ui-file-icon.
const iconColor=(svg:Element)=>(svg.closest('[data-syno-pkg-land-ui]') as HTMLElement).style.color
test('tree and table draw File Station icons from the UI theme tokens',async()=>{
 const root=mount(FileBrowser,fixture());await settle()
 root.querySelector<HTMLElement>('[data-tree-node="/homes"]')!.click();await settle()
 const icon=(id:string)=>Array.from(root.querySelectorAll(`[data-tree-node="${id}"] svg`)).at(-1)!
 expect([icon('/homes'),icon('/shared'),row(root,'/homes/sub').querySelector('svg')!].map(iconColor)).toEqual(Array(3).fill('var(--ui-file-folder)'))
 expect(iconColor(row(root,'/homes/a.txt').querySelector('svg')!)).toBe('var(--ui-file-icon)')
})
test('a selected tree folder only shades its row, as File Station does',async()=>{
 const root=mount(FileBrowser,fixture());await settle()
 root.querySelector<HTMLElement>('[data-tree-node="/homes"]')!.click();await settle()
 const node=root.querySelector<HTMLElement>('[data-tree-node="/homes"][data-selected]')!
 expect([node.className,node.querySelector('svg')!.closest('[data-syno-pkg-land-ui]')!.className].join(' ')).not.toContain('--ui-primary')
})
test('an empty folder shows the empty state inside the fixed-height listing',async()=>{
 const root=mount(FileBrowser,fixture());await settle()
 dblclick(row(root,'/shared'));await settle()
 const listing=root.querySelector<HTMLElement>('.file-content')!
 expect(listing.textContent).toContain(nasCopy('en').emptyFolder)
 expect(iconColor(listing.querySelector('.file-empty svg')!)).toBe('var(--ui-file-folder)')
})
test('tree nodes keep their icon component out of Vue reactivity',async()=>{
 const warn=vi.spyOn(console,'warn').mockImplementation(()=>{});cleanup.push(()=>warn.mockRestore())
 const root=mount(FileBrowser,fixture());await settle()
 button(root,'Expand homes').click();await settle()
 expect(warn.mock.calls.map(([m])=>String(m)).filter(m=>m.includes('made a reactive object'))).toEqual([])
})
test('picker states the DSM session notice once',async()=>{
 const root=mount(NasPicker,fixture(),{open:true,mode:'folder'});await settle()
 expect(root.textContent!.split(nasCopy('en').ownPermissions)).toHaveLength(2)
})
test.each([407,408,'denied'])('permission error %s is visible and suppresses misleading empty state',async(code)=>{
 const call=vi.fn(async()=>{throw {code,message:'secret SID'}}),root=mount(FileBrowser,call);await settle()
 expect(root.querySelector('[role="alert"]')?.textContent).toContain(nasCopy('en').errorDenied)
 expect(root.textContent).not.toContain('secret SID')
 expect(root.textContent).not.toContain(nasCopy('en').emptyFolder)
 expect(button(root,nasCopy('en').refresh).disabled).toBe(false)
})
test('folder navigation clears selection when permissions are lost',async()=>{
 const call=fixture(),root=mount(FileBrowser,call,{mode:'file'});await settle()
 dblclick(row(root,'/homes'));await settle();click(row(root,'/homes/a.txt'));await settle()
 call.mockRejectedValue({code:'denied'});button(root,nasCopy('en').refresh).click();await settle()
 expect(root.querySelector('[role="alert"]')).toBeTruthy()
 expect(root.textContent).not.toContain('a.txt')
})

test('lazy tree follows full DSM page cursors even when a page contains only files',async()=>{
 const call=vi.fn(async(method:string,params:any)=>{
  if(method==='nas.files.shares')return {shares:[folder('/team')],offset:0,total:1}
  if(params.offset===0)return {files:[file('/team/a.txt')],offset:0,total:2}
  return {files:[folder('/team/later')],offset:1,total:2}
 })
 const root=mount(FileBrowser,call);await settle();button(root,'Expand team').click();await settle()
 expect(call.mock.calls.filter(([m])=>m==='nas.files.list').map(([,p])=>p.offset)).toEqual([0,1])
 expect(root.textContent).toContain('later')
})
test('reopening picker clears the old path and reloads current DSM visibility',async()=>{
 const call=fixture(),open=ref(true),selected=vi.fn(),root=document.createElement('div');document.body.append(root)
 const provider=createNasProvider({call:call as any})
 const app=createApp({render:()=>h(AppRoot,{},()=>h(NasPicker,{provider,locale:'en',mode:'folder',open:open.value,onSelected:selected,'onUpdate:open':v=>{open.value=v}}))})
 app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 await settle();click(row(root,'/homes'));await settle();button(root,nasCopy('en').confirm).click();await settle()
 expect(selected).toHaveBeenCalledWith('/homes')
 open.value=true;await settle()
 expect(button(root,nasCopy('en').confirm).disabled).toBe(true)
 expect(call.mock.calls.filter(([m])=>m==='nas.files.shares')).toHaveLength(2)
})

test('upload picker states the same 16 GB limit as the size error',async()=>{
 const call=fixture(),provider=createNasProvider({call:call as any})
 const root=mount(FileBrowser,call,{transfers:true,provider:{...provider,upload:vi.fn(),download:vi.fn(),info:vi.fn()}});await settle()
 dblclick(row(root,'/homes'));await settle()
 expect(nasCopy('en').errorUploadSize).toContain('16 GB')
 expect(root.textContent).toContain('max 16 GB')
})
test('file browser downloads selected files and streams uploads to the current folder',async()=>{
 const call=fixture(),provider=createNasProvider({call:call as any}),upload=vi.fn(async()=>{}),download=vi.fn(),info=vi.fn(async()=>({name:'a.txt',path:'/homes/a.txt',isDir:false}))
 const root=mount(FileBrowser,call,{transfers:true,provider:{...provider,upload,download,info}});await settle()
 dblclick(row(root,'/homes'));await settle();click(row(root,'/homes/a.txt'));await settle()
 button(root,nasCopy('en').download).click();await settle();expect(info).toHaveBeenCalledWith('/homes/a.txt',expect.any(AbortSignal));expect(download).toHaveBeenCalledWith('/homes/a.txt')
 const picker=root.querySelector<HTMLInputElement>('input[type=file]')!;expect(picker).toBeTruthy()
 const file=new File(['payload'],'upload.txt');Object.defineProperty(picker,'files',{value:[file],configurable:true});picker.dispatchEvent(new Event('change',{bubbles:true}));await settle()
 button(root,nasCopy('en').upload).click();await settle()
 expect(upload).toHaveBeenCalledWith('/homes',file,expect.any(AbortSignal));expect(root.textContent).toContain(nasCopy('en').uploaded)
})
test.each([['en','Size','34.3 KB','1.5 GB'],['de','Größe','34,3 KB','1,5 GB']])('the size column is human-readable in %s and folders show a dash',async(locale,header,kb,gb)=>{
 const call=vi.fn(async(method:string)=>{
  if(method==='nas.files.shares')return {shares:[folder('/homes')],offset:0,total:1}
  return {files:[folder('/homes/sub'),{...file('/homes/a.txt'),additional:{size:35149}},{...file('/homes/b.iso'),additional:{size:1.5*1024**3}},{...file('/homes/c.txt'),additional:{size:512}}],offset:0,total:4}
 })
 const root=mount(FileBrowser,call,{locale});await settle()
 dblclick(row(root,'/homes'));await settle()
 const size=(path:string)=>row(root,path).querySelector('.file-bytes')!.textContent
 expect([size('/homes/sub'),size('/homes/a.txt'),size('/homes/b.iso'),size('/homes/c.txt')]).toEqual(['—',kb,gb,'512 B'])
 expect(Array.from(root.querySelectorAll('th')).map(th=>th.textContent?.trim())).toContain(header)
})
test('the tree expands to and selects the open folder however it was entered',async()=>{
 const root=mount(FileBrowser,fixture());await settle()
 const selected=()=>Array.from(root.querySelectorAll<HTMLElement>('[data-tree-node][data-selected]')).map(n=>n.dataset.treeNode)
 dblclick(row(root,'/homes'));await settle()
 expect(selected()).toEqual(['/homes'])
 dblclick(row(root,'/homes/sub'));await settle()
 expect(button(root,'Collapse homes')).toBeTruthy()
 expect(selected()).toEqual(['/homes/sub'])
 crumbs(root)[1].click();await settle()
 expect(selected()).toEqual(['/homes'])
 crumbs(root)[0].click();await settle()
 expect(selected()).toEqual([])
})
test('back and forward walk the visited folders like File Station',async()=>{
 const root=mount(FileBrowser,fixture());await settle()
 const copy=nasCopy('en'),back=()=>button(root,copy.back),forward=()=>button(root,copy.forward)
 const open=()=>current(root)?.textContent?.trim()
 expect([back().disabled,forward().disabled]).toEqual([true,true])
 expect(forward().textContent?.trim()).toBe('')
 dblclick(row(root,'/homes'));await settle();dblclick(row(root,'/homes/sub'));await settle()
 back().click();await settle()
 expect([open(),forward().disabled]).toEqual(['homes',false])
 back().click();await settle()
 expect([open(),back().disabled]).toEqual([copy.roots,true])
 forward().click();await settle();forward().click();await settle()
 expect([open(),forward().disabled]).toEqual(['sub',true])
 back().click();await settle()
 crumbs(root)[0].click();await settle()
 expect([open(),forward().disabled,back().disabled]).toEqual([copy.roots,true,false])
})
