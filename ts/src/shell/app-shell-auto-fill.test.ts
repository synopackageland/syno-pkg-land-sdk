// @vitest-environment jsdom
// A page that shows a SettingsPage or a SectionTabs fill fills the height by
// itself, however the App wraps it, and a settings footer is the window's
// bottom edge whether or not the SettingsPage sits in tabs.
import {readFileSync} from 'node:fs'
import {compileStyle,parse} from '@vue/compiler-sfc'
import {afterEach,expect,test} from 'vitest'
import {createApp,defineComponent,h,nextTick,ref,vShow,withDirectives,type Component} from 'vue'
import {SectionTabs,SettingsPage} from '@synopackageland/ui'
import AppShell from './AppShell.vue'
import type {AppShellPageEntry} from './app-pages'

// jsdom has neither; SectionTabs measures its indicator with them.
globalThis.CSS??={escape:(value:string)=>value} as never
globalThis.ResizeObserver??=class{observe(){}unobserve(){}disconnect(){}} as never

const tabs=[{value:'general',label:'General'},{value:'about',label:'About'}]
const Settings=defineComponent({render:()=>h(SettingsPage,null,{default:()=>h('p','Sections'),footer:()=>h('button','Apply')})})
const Tabbed=defineComponent({render:()=>h(SectionTabs,{modelValue:'general',items:tabs,fill:true},{'panel-general':()=>h(Settings)})})
// The template's page wrappers: nothing in the App passes the height on.
const wrapped=(page:Component)=>defineComponent({render:()=>h('div',{class:'integration-app'},[h('div',{'data-page':'settings'},[h('h2','Hidden heading'),h(page)])])})

const mounted:(()=>void)[]=[]
afterEach(()=>{for(const unmount of mounted.splice(0)) unmount();document.head.innerHTML=''})
async function mount(pages:AppShellPageEntry[],modelValue='settings',slots:Record<string,unknown>={}){
 const root=document.createElement('div');document.body.append(root)
 const current=ref(modelValue)
 const app=createApp({render:()=>h(AppShell,{locale:'en',modelValue:current.value,pages},slots)})
 app.mount(root);await nextTick();await nextTick()
 mounted.push(()=>{app.unmount();root.remove()})
 const page=()=>root.querySelector('.app-page')!
 return {root,current,fill:()=>page().classList.contains('app-page-fill'),flush:()=>page().classList.contains('app-page-flush'),shell:()=>root.querySelector('.app-shell')!.classList.contains('app-shell-fill')}
}

test.each([
 ['a SettingsPage', Settings],
 ['a SectionTabs fill with a SettingsPage', Tabbed],
 ['a SettingsPage inside App wrappers', wrapped(Settings)],
 ['a SectionTabs fill inside App wrappers', wrapped(Tabbed)],
])('a page that shows %s fills the height and drops the space under its footer',async(_,component)=>{
 const shell=await mount([{id:'settings',title:'Settings',component}])
 expect(shell.fill()).toBe(true)
 expect(shell.shell()).toBe(true)
 expect(shell.flush()).toBe(true)
})

test('a SettingsPage without a footer fills but keeps the space under the page',async()=>{
 const Plain=defineComponent({render:()=>h(SettingsPage,null,{default:()=>h('p','Read only')})})
 const shell=await mount([{id:'settings',title:'Settings',component:Plain}])
 expect(shell.fill()).toBe(true)
 expect(shell.flush()).toBe(false)
})

test('any other page scrolls as a whole, and fill: false keeps a settings page scrolling',async()=>{
 const other=await mount([{id:'settings',title:'Settings',component:defineComponent({render:()=>h('p','Long text')})}])
 expect(other.fill()).toBe(false)
 expect(other.shell()).toBe(false)
 const kept=await mount([{id:'settings',title:'Settings',component:Tabbed,fill:false}])
 expect(kept.fill()).toBe(false)
 expect(kept.flush()).toBe(true)
})

// A page the default slot keeps mounted but hidden must not make the current page fill.
test('a settings page kept mounted and hidden by v-show or hidden does not count',async()=>{
 const body=(page:string)=>[h('p',{class:'status'},'Status'),withDirectives(h('div',[h(Tabbed)]),[[vShow,page==='settings']]),h('div',{hidden:true},[h(Settings)])]
 const shell=await mount([{id:'status',title:'Status'},{id:'settings',title:'Settings'}],'status',{default:({page}:{page:string})=>body(page)})
 expect(shell.fill()).toBe(false)
 expect(shell.flush()).toBe(false)
 shell.current.value='settings';await nextTick();await nextTick()
 expect(shell.fill()).toBe(true)
 expect(shell.flush()).toBe(true)
 shell.current.value='status';await nextTick();await nextTick()
 expect(shell.fill()).toBe(false)
})

// jsdom has no layout: read what AppShell's scoped style assigns to the App's
// wrappers on a fill page (as app-page-fill.test.ts does).
test("the App's wrappers pass the height to the SettingsPage without CSS of their own",async()=>{
 const id='data-v-auto'
 const source=readFileSync('src/shell/AppShell.vue','utf8')
 const css=parse(source).descriptor.styles.filter(style=>style.scoped).map(style=>compileStyle({source:style.content,filename:'AppShell.vue',id,scoped:true}).code).join('\n')
 const sheet=document.createElement('style');sheet.textContent=css;document.head.append(sheet)
 const page=document.createElement('div');page.className='app-page app-page-fill';page.setAttribute(id,'')
 page.innerHTML=`<div class="app-page-content" ${id}><p class="note">Note</p><div class="app"><div class="callout">Error</div><div data-page="settings"><div class="ui-section-tabs-fill" data-ui-section-tabs-fill><nav></nav><div class="ui-tab-panel"><div data-ui-settings-page></div></div></div></div><div data-page="other" hidden><div data-ui-settings-page></div></div></div><div class="ui-section-tabs-fill" data-ui-section-tabs-fill><nav></nav><div class="ui-tab-panel about"><p>About</p></div></div></div>`
 document.body.append(page)
 try{
  const style=(selector:string)=>getComputedStyle(page.querySelector(selector)!)
  for(const selector of ['.app','[data-page=settings]']){
   expect(style(selector).display).toBe('flex')
   expect(style(selector).flexDirection).toBe('column')
   expect(style(selector).flexGrow).toBe('1')
   expect(style(selector).minHeight).toBe('0px')
  }
  expect(style('[data-page=other]').display).toBe('none')
  expect(style('.callout').display).not.toBe('flex')
  expect(style('.note').flexGrow).toBe('0')
  // A tab that is not a SettingsPage scrolls under its strip; a SettingsPage scrolls its own body.
  expect(style('.ui-tab-panel.about').overflowY).toBe('auto')
  expect(style('[data-page=settings] .ui-tab-panel').overflowY).not.toBe('auto')
 }finally{page.remove()}
})
