// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,defineComponent,h,nextTick,ref,type App,type Component} from 'vue'
import type {DropdownMenuEntry} from '@synopackageland/ui'
import AppShell from './AppShell.vue'
import PageMeta from './PageMeta.vue'
import PageInfo from './PageInfo.vue'
import PageActions from './PageActions.vue'
import * as vue from '../vue'

globalThis.ResizeObserver??=class{observe(){}unobserve(){}disconnect(){}} as never

async function settle(){await new Promise(resolve=>setTimeout(resolve));for(let i=0;i<6;i++)await nextTick()}
const apps:App[]=[]
function mount(render:()=>unknown){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render});app.mount(root);apps.push(app)
 return root
}
afterEach(async()=>{for(const app of apps.splice(0))app.unmount();await settle();document.body.replaceChildren();vi.restoreAllMocks();vi.unstubAllGlobals()})
function viewport(width:number,coarse=false){
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width} as DOMRect)
 const media=new EventTarget() as MediaQueryList
 Object.assign(media,{matches:coarse,media:'(pointer: coarse)',addListener:()=>{},removeListener:()=>{}})
 vi.stubGlobal('matchMedia',vi.fn(()=>media))
}
const shell=(page:Component,props:Record<string,unknown>={},slots:Record<string,unknown>={},header?:boolean)=>()=>h(AppShell,{locale:'en',modelValue:'volumes',pages:[{id:'volumes',title:'Volumes',component:page,header},{id:'settings',title:'Settings'}],...props},slots)
const picker=()=>h('select',{class:'volume'},[h('option','volume1'),h('option','volume2')])
const menu:DropdownMenuEntry[]=[{key:'volume',label:'Volume',children:[{key:'volume:1',label:'volume1',checked:true},{key:'volume:2',label:'volume2'}]}]
const info=(root:ParentNode)=>root.querySelector('button[aria-label^="Notes"]')!
const before=(a:Node,b:Node)=>!!(a.compareDocumentPosition(b)&Node.DOCUMENT_POSITION_FOLLOWING)

test.each([
 {header:undefined,where:'[data-app-page-bar] .app-page-meta'},
 {header:true,where:'[data-ui-page-header] [data-ui-page-header-meta]'},
])('a page puts its controls in $where, before its notes and the App meta',async({header,where})=>{
 const Page=defineComponent({render:()=>[h(PageInfo,{notes:['Sizes are estimates.']}),h(PageMeta,null,picker),h(PageActions,null,()=>h('button',{class:'rescan'},'Rescan')),h('p','Body')]})
 const root=mount(shell(Page,{},{meta:()=>h('span',{class:'scanned'},'Scanned')},header))
 await settle()
 const meta=root.querySelector(where)!
 const control=meta.querySelector('select.volume')!
 expect(control).not.toBeNull()
 expect(before(control,info(meta))).toBe(true)
 expect(before(info(meta),meta.querySelector('.scanned')!)).toBe(true)
 expect(root.querySelector('.app-page-content select')).toBeNull()
 // The page's actions keep their place.
 expect(meta.querySelector('.rescan')).toBeNull()
 expect(root.querySelector('.rescan')).not.toBeNull()
})

test('the controls show the page bar alone, and leave it with their page',async()=>{
 const Page=defineComponent({render:()=>h(PageMeta,null,picker)})
 const current=ref('volumes')
 const root=mount(()=>h(AppShell,{locale:'en',modelValue:current.value,pages:[{id:'volumes',title:'Volumes',component:Page},{id:'settings',title:'Settings'}]}))
 await settle()
 const bar=()=>root.querySelector<HTMLElement>('[data-app-page-bar]')!
 expect(bar().hidden).toBe(false)
 expect(bar().querySelector('select.volume')).not.toBeNull()
 current.value='settings';await settle()
 expect(root.querySelector('select.volume')).toBeNull()
 expect(bar().hidden).toBe(true)
})

test('on desktop a menu adds nothing to the bar, and the controls stay',async()=>{
 viewport(1200)
 const Page=defineComponent({render:()=>h(PageMeta,{menu},picker)})
 const root=mount(shell(Page,{appRoot:true}))
 await settle()
 expect(root.querySelector('[data-ui-layout-mode="mobile"]')).toBeNull()
 expect(root.querySelector('[data-app-page-bar] select.volume')).not.toBeNull()
})

test('in the mobile layout the menu takes the controls\' place in the bar\'s "…", before the App\'s actions',async()=>{
 viewport(390,true)
 const Page=defineComponent({render:()=>h(PageMeta,{menu},picker)})
 const root=mount(shell(Page,{appRoot:true,headerActions:[{key:'export',label:'Export'},{key:'help',label:'Help'}]}))
 await settle()
 expect(root.querySelector('[data-ui-layout-mode="mobile"]')).not.toBeNull()
 expect(root.querySelector('select.volume')).toBeNull()
 root.querySelector<HTMLButtonElement>('[data-ui-mobile-more]')!.click();await settle()
 const labels=Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).map(item=>item.textContent?.trim())
 expect(labels.slice(0,3)).toEqual(['Volume','Export','Help'])
})

test('a selection from the page\'s entries goes to the page, not the App',async()=>{
 viewport(390,true)
 const selected=vi.fn(),appAction=vi.fn()
 const flat:DropdownMenuEntry[]=[{key:'volume:1',label:'volume1',checked:true},{key:'volume:2',label:'volume2'}]
 const Page=defineComponent({render:()=>h(PageMeta,{menu:flat,onSelect:selected},picker)})
 const root=mount(shell(Page,{appRoot:true,headerActions:[{key:'export',label:'Export'},{key:'help',label:'Help'}],onHeaderAction:appAction}))
 await settle()
 const choose=async(label:string)=>{
  root.querySelector<HTMLButtonElement>('[data-ui-mobile-more]')!.click();await settle()
  const entry=Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find(item=>item.textContent?.trim()===label)
  expect(entry).toBeDefined()
  entry!.click();await settle()
 }
 await choose('volume2')
 expect(selected).toHaveBeenCalledExactlyOnceWith('volume:2')
 expect(appAction).not.toHaveBeenCalled()
 await choose('Export')
 expect(appAction).toHaveBeenCalledExactlyOnceWith('export')
 expect(selected).toHaveBeenCalledOnce()
})

test('in the mobile layout controls without a menu stay in the page bar',async()=>{
 viewport(390,true)
 const Page=defineComponent({render:()=>h(PageMeta,null,picker)})
 const root=mount(shell(Page,{appRoot:true}))
 await settle()
 expect(root.querySelector('[data-ui-layout-mode="mobile"]')).not.toBeNull()
 const bar=root.querySelector<HTMLElement>('[data-app-page-bar]')!
 expect(bar.hidden).toBe(false)
 expect(bar.querySelector('select.volume')).not.toBeNull()
 expect(root.querySelector('[data-ui-mobile-more]')).toBeNull()
})

test('the menu leaves the bar with its page',async()=>{
 viewport(390,true)
 const Page=defineComponent({render:()=>h(PageMeta,{menu},picker)})
 const current=ref('volumes')
 const root=mount(()=>h(AppShell,{appRoot:true,locale:'en',modelValue:current.value,headerActions:[{key:'export',label:'Export'},{key:'help',label:'Help'}],pages:[{id:'volumes',title:'Volumes',component:Page},{id:'settings',title:'Settings'}]}))
 await settle()
 const labels=async()=>{
  root.querySelector<HTMLButtonElement>('[data-ui-mobile-more]')!.click();await settle()
  const shown=Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).map(item=>item.textContent?.trim())
  document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));await settle()
  return shown
 }
 expect(await labels()).toContain('Volume')
 current.value='settings';await settle()
 expect(await labels()).not.toContain('Volume')
})

test('outside an AppShell the controls render nothing',async()=>{
 const root=mount(()=>h(PageMeta,{menu},picker))
 await settle()
 expect(root.querySelector('select')).toBeNull()
})

test('the package exports PageMeta and its target, so a page can be mounted without the shell',async()=>{
 expect(vue.PageMeta).toBe(PageMeta)
 expect(vue.PageMetaTarget).toBeTypeOf('symbol')
 expect(vue.PageMetaMenu).toBeTypeOf('symbol')
 const Page=defineComponent({render:()=>h(PageMeta,null,picker)})
 const root=document.createElement('div'),target=document.createElement('div');document.body.append(root,target)
 const app=createApp({render:()=>h(Page)});apps.push(app)
 app.provide(vue.PageMetaTarget,ref(target))
 app.mount(root);await settle()
 expect(target.querySelector('select.volume')).not.toBeNull()
})
