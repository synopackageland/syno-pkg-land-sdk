// @vitest-environment jsdom
import {expect,test} from 'vitest'
import {createApp,defineComponent,h,nextTick,ref} from 'vue'
import AppShell from './AppShell.vue'
import PageActions from './PageActions.vue'
import * as vue from '../vue'

// A page registered as a component cannot fill the shell's #actions slot; it
// puts its own buttons in the header with PageActions, before the app's.
test('a page component puts its own actions in the page header, before the app actions',async()=>{
 const Page=defineComponent({render:()=>[h(PageActions,null,()=>h('button',{class:'rescan'},'Rescan')),h('p','Body')]})
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{locale:'en',modelValue:'packages',pages:[{id:'packages',title:'Packages',component:Page}]},{actions:()=>h('button',{class:'new-scan'},'New scan')})})
 app.mount(root);await nextTick();await nextTick()
 const header=root.querySelector('[data-ui-page-header]')!
 expect(Array.from(header.querySelectorAll('button')).map(button=>button.className)).toEqual(['rescan','new-scan'])
 expect(root.querySelector('.app-page-content .rescan')).toBeNull()
 app.unmount();root.remove()
})

test('the actions leave the header with their page',async()=>{
 const Page=defineComponent({render:()=>h(PageActions,null,()=>h('button',{class:'rescan'},'Rescan'))})
 const current=ref('packages')
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{locale:'en',modelValue:current.value,pages:[{id:'packages',title:'Packages',component:Page},{id:'settings',title:'Settings'}]})})
 app.mount(root);await nextTick();await nextTick()
 expect(root.querySelector('[data-ui-page-header] .rescan')).not.toBeNull()
 current.value='settings';await nextTick();await nextTick()
 expect(root.querySelector('.rescan')).toBeNull()
 app.unmount();root.remove()
})

test('outside an AppShell the actions render nothing',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(PageActions,null,()=>h('button','Rescan'))})
 app.mount(root);await nextTick()
 expect(root.innerHTML).toBe('<!--v-if-->')
 app.unmount();root.remove()
})

test('PageActions is exported with the shell',()=>{
 expect(vue.PageActions).toBe(PageActions)
})

// a page tested on its own provides the target its PageActions render into.
test('the package exports the target, so a page can be mounted without the shell',async()=>{
 expect(vue.PageActionsTarget).toBeTypeOf('symbol')
 const Page=defineComponent({render:()=>h(PageActions,null,()=>h('button',{class:'rescan'},'Rescan'))})
 const root=document.createElement('div'),target=document.createElement('div');document.body.append(root,target)
 const app=createApp({render:()=>h(Page)})
 app.provide(vue.PageActionsTarget,ref(target))
 app.mount(root);await nextTick()
 expect(target.querySelector('.rescan')).not.toBeNull()
 app.unmount();root.remove();target.remove()
})
// the shell passes PageHeader's meta slot on, beside the title.
test('the shell forwards the page header meta slot',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{locale:'en',modelValue:'packages',pages:[{id:'packages',title:'Packages'}]},{meta:({page}:{page:string})=>h('span',{class:'scanned'},page)})})
 app.mount(root);await nextTick()
 expect(root.querySelector('[data-ui-page-header] .scanned')?.textContent).toBe('packages')
 app.unmount();root.remove()
})
