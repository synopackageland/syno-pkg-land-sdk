// @vitest-environment jsdom
import {afterEach,expect,test} from 'vitest'
import {createApp,defineComponent,h,nextTick} from 'vue'
import {Callout} from '@synopackageland/ui'
import AppShell from './AppShell.vue'
import PageInfo from './PageInfo.vue'

const flush=async()=>{for(let i=0;i<4;i++)await nextTick()}
const mounted:(()=>void)[]=[]
afterEach(()=>{for(const unmount of mounted.splice(0))unmount();try{localStorage.clear()}catch{}})

function mount(body:()=>unknown){
 const Page=defineComponent({render:body})
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{locale:'en',modelValue:'home',pages:[{id:'home',title:'Home',component:Page}]})})
 app.mount(root);mounted.push(()=>{app.unmount();root.remove()})
 return {root,bar:()=>root.querySelector<HTMLElement>('[data-app-page-bar]')!}
}

// A page with no actions or notes keeps the bar hidden; once a callout is
// dismissed, the bar shows the UI's single button for it, and only that.
test('a page with no buttons shows the hidden-notices button only while a callout is dismissed',async()=>{
 const shell=mount(()=>h(Callout,{tone:'info',dismissible:true,dismissKey:'shell-recall-test'},()=>'Tip'))
 await flush()
 expect(shell.bar().hidden).toBe(true)
 shell.root.querySelector<HTMLButtonElement>('[data-ui-callout-close]')!.click()
 await flush()
 expect(shell.bar().hidden).toBe(false)
 expect(shell.bar().querySelectorAll('[data-ui-callout-recall-trigger]')).toHaveLength(1)
 expect(shell.bar().querySelectorAll('button')).toHaveLength(1)
 shell.bar().querySelector<HTMLElement>('[data-ui-callout-recall-trigger]')!.click()
 await flush()
 document.querySelector<HTMLButtonElement>('[data-ui-callout-restore]')!.click()
 await flush()
 expect(shell.bar().querySelector("[data-ui-callout-recall-trigger]")).toBeNull()
 expect(shell.bar().hidden).toBe(true)
})

// A page that already shows an info button keeps that one: no second icon.
test('a page info button takes the list instead of a second button',async()=>{
 const shell=mount(()=>[h(PageInfo,null,()=>'About this page'),h(Callout,{tone:'info',dismissible:true,dismissKey:'shell-recall-info'},()=>'Tip')])
 await flush()
 shell.root.querySelector<HTMLButtonElement>('[data-ui-callout-close]')!.click()
 await flush()
 expect(shell.bar().hidden).toBe(false)
 expect(shell.bar().querySelector('[data-ui-callout-recall-trigger]')).toBeNull()
 expect(Array.from(shell.bar().querySelectorAll('button')).map(button=>button.getAttribute('aria-label'))).toEqual(['Notes'])
})
