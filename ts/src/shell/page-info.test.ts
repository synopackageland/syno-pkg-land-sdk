// @vitest-environment jsdom
import {afterEach,expect,test} from 'vitest'
import {createApp,defineComponent,h,nextTick,ref,type App,type Component} from 'vue'
import AppShell from './AppShell.vue'
import PageInfo from './PageInfo.vue'
import * as vue from '../vue'

// jsdom has no ResizeObserver; the notes' tooltip measures itself with one.
globalThis.ResizeObserver??=class{observe(){}unobserve(){}disconnect(){}} as never

const notes=['Sizes count only the data snapshots hold.','This report is 15 days old.']
// InfoTip opens on keyboard focus after a timer turn, past any pointer press.
async function settle(){await new Promise(resolve=>setTimeout(resolve));for(let i=0;i<6;i++)await nextTick()}
const apps:App[]=[]
function mount(render:()=>unknown){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render});app.mount(root);apps.push(app)
 return {root,app}
}
afterEach(async()=>{for(const app of apps.splice(0))app.unmount();await settle();document.body.replaceChildren()})
const shell=(page:Component,props:Record<string,unknown>={},slots:Record<string,unknown>={})=>()=>h(AppShell,{locale:'en',modelValue:'snapshots',pages:[{id:'snapshots',title:'Snapshots',component:page},{id:'settings',title:'Settings'}],...props},slots)
const button=(root:ParentNode)=>root.querySelector<HTMLButtonElement>('button[aria-label^="Notes"],button[aria-label^="附註"]')

// Reclaim teleported its notes to the shell's #meta slot itself.
test('a page puts its notes on the title line, after the title and before the App meta',async()=>{
 const Page=defineComponent({render:()=>[h(PageInfo,{notes}),h('p','Body')]})
 const {root}=mount(shell(Page,{},{meta:()=>h('span',{class:'scanned'},'Scanned')}))
 await settle()
 const meta=root.querySelector('[data-ui-page-header] [data-ui-page-header-meta]')!
 expect(meta.parentElement!.firstElementChild!.tagName).toBe('H1')
 expect(button(meta)).not.toBeNull()
 expect(button(meta)!.compareDocumentPosition(meta.querySelector('.scanned')!)&Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
 expect(button(root.querySelector('.app-page-content')!)).toBeNull()
})

test('the notes leave the header with their page',async()=>{
 const current=ref('snapshots')
 const Page=defineComponent({render:()=>h(PageInfo,{notes})})
 mount(()=>h(AppShell,{locale:'en',modelValue:current.value,pages:[{id:'snapshots',title:'Snapshots',component:Page},{id:'settings',title:'Settings'}]}))
 await settle()
 expect(button(document)).not.toBeNull()
 current.value='settings';await settle()
 expect(button(document)).toBeNull()
})

test('an inline PageInfo stays beside the control its notes explain',async()=>{
 const Page=defineComponent({render:()=>h('div',{class:'search'},h(PageInfo,{notes,inline:true}))})
 const {root}=mount(shell(Page))
 await settle()
 expect(button(root.querySelector('.search')!)).not.toBeNull()
 expect(button(root.querySelector('[data-ui-page-header]')!)).toBeNull()
})

test('outside an AppShell it stays where the page puts it',async()=>{
 const {root}=mount(()=>h('div',{class:'page'},h(PageInfo,{notes})))
 await settle()
 expect(button(root.querySelector('.page')!)).not.toBeNull()
})

test('a page without notes shows no button',async()=>{
 const Page=defineComponent({render:()=>h(PageInfo,{notes:['','']})})
 const {root}=mount(shell(Page))
 await settle()
 expect(button(root)).toBeNull()
})

// The button names its notes in the shell's language; the App may name it itself.
test('the button is named by the number of notes, in the shell language',async()=>{
 const Page=defineComponent({render:()=>h(PageInfo,{notes})})
 const {root}=mount(shell(Page,{locale:'zh-TW'}))
 await settle()
 expect(button(root)!.getAttribute('aria-label')).toBe('附註（2）')
 const {root:named}=mount(()=>h(PageInfo,{notes,label:'About this report'}))
 await settle()
 expect(named.querySelector('button')!.getAttribute('aria-label')).toBe('About this report')
 const {root:english}=mount(()=>h(PageInfo,{notes:[notes[0]!],locale:'en'}))
 await settle()
 expect(english.querySelector('button')!.getAttribute('aria-label')).toBe('Notes (1)')
})

test('the notes open on activation, below the button from its start, with an info mark',async()=>{
 const {root}=mount(()=>h(PageInfo,{notes}))
 await settle()
 const trigger=root.querySelector('button')!
 expect(trigger.querySelector('svg')!.classList).toContain('lucide-info')
 expect(trigger.getAttribute('aria-expanded')).toBe('false')
 trigger.click();await settle()
 expect(trigger.getAttribute('aria-expanded')).toBe('true')
 const tip=document.querySelector('[role="tooltip"]')!
 expect(Array.from(tip.querySelectorAll('li'),item=>item.textContent)).toEqual(notes)
 const content=tip.closest('[data-side]')
 expect(content?.getAttribute('data-side')).toBe('bottom')
 expect(content?.getAttribute('data-align')).toBe('start')
})

test('the keyboard opens the notes and Escape closes them',async()=>{
 const {root}=mount(()=>h(PageInfo,{notes}))
 await settle()
 const trigger=root.querySelector('button')!
 trigger.focus();await settle()
 expect(trigger.getAttribute('aria-expanded')).toBe('true')
 document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}))
 await settle()
 expect(trigger.getAttribute('aria-expanded')).toBe('false')
})

test('the slot replaces the list of notes',async()=>{
 const {root}=mount(()=>h(PageInfo,null,()=>h('p',{class:'own'},'Own notes')))
 await settle()
 root.querySelector('button')!.click();await settle()
 expect(document.querySelector('[role="tooltip"] .own')?.textContent).toBe('Own notes')
})

// pattern: a page tested on its own provides the target.
test('PageInfo and its target are exported with the shell',async()=>{
 expect(vue.PageInfo).toBe(PageInfo)
 expect(vue.PageInfoTarget).toBeTypeOf('symbol')
 const target=document.createElement('div');document.body.append(target)
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(PageInfo,{notes})})
 app.provide(vue.PageInfoTarget,ref(target))
 app.mount(root);apps.push(app);await settle()
 expect(button(target)).not.toBeNull()
})
