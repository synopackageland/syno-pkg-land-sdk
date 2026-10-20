// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import AppShell from './AppShell.vue'

// With the real UI components: the notices share the page body, and so the
// padding, with the page header; app-root gives the shell its own app root.
test('in the real page the notices sit beside the header inside the page body',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{appRoot:true,locale:'en',modelValue:'home',pages:[{id:'home',title:'Home'}]},{notices:()=>h('p',{class:'notice'},'Saved')})})
 app.config.idPrefix='shell-ui';app.mount(root);await nextTick()
 // The shell fills the root: no element of the locale's provider sits between them.
 expect(root.querySelector('[data-ui-app] > [data-ui-background] > .app-shell')).not.toBeNull()
 const body=root.querySelector('[data-ui-app] [data-page-content-body]')!
 expect(body.querySelector(':scope > [data-ui-page-header]')).not.toBeNull()
 expect(body.querySelector(':scope > .app-notices > .notice')?.textContent).toBe('Saved')
 app.unmount();root.remove()
})

// A DSM window narrowed to the mobile layout shows a phone bar; DSM's own page
// title ("vtnas - Synology NAS") and icon are not the App's.
test('a narrow DSM window names the App in the phone bar, not the DSM page title',async()=>{
 document.title='vtnas - Synology NAS'
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width:390} as DOMRect)
 const host={platform:()=>({web:true,host:'dsm' as const,dsmStandalone:false,dsmLanguage:null,appLocale:null,systemVersion:null,fullVersion:null,theme:null})}
 const ctx={call:async()=>undefined as never,reopen:()=>()=>{},locale:'en',host,displayName:'Connect',appIcon:'webman/3rdparty/Connect/images/icon_64.png'}
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{ctx,locale:'en',modelValue:'home',pages:[{id:'home',title:'Home'}]})})
 app.mount(root);await nextTick();await nextTick()
 expect(root.textContent).toContain('Connect')
 expect(root.textContent).not.toContain('Synology NAS')
 expect(root.querySelector('img')?.getAttribute('src')).toBe('webman/3rdparty/Connect/images/icon_64.png')
 app.unmount();root.remove();document.title='';vi.restoreAllMocks()
})

// DSM renders its portal header in the same document, outside the App root.
// Merely being standalone is insufficient: remove_banner leaves hidden nodes.
test('a visible DSM portal header removes duplicate branding and the default account only',async()=>{
 const {portalFixture}=await import('./portal-chrome.test-fixture')
 const fixture=portalFixture()
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width:1400,height:39} as DOMRect)
 const root=document.createElement('div');fixture.main.append(root)
 const props={locale:'en',modelValue:'home',pages:[{id:'home',title:'Home'}],ctx:{fullPage:true as const,displayName:'Portal App',user:{name:'alice',isAdmin:false}}}
 const app=createApp({render:()=>h(AppShell,props)})
 app.mount(root);await nextTick();await nextTick()
 expect(root.querySelector('.app-shell')?.classList.contains('app-shell-portal-brand')).toBe(true)
 expect(root.textContent).not.toContain('alice')
 fixture.main.classList.add('sds-standalone-main-window--no-banner')
 await new Promise(resolve=>setTimeout(resolve,0));await nextTick()
 expect(root.querySelector('.app-shell')?.classList.contains('app-shell-portal-brand')).toBe(false)
 expect(root.textContent).toContain('alice')
 app.unmount();fixture.remove();vi.restoreAllMocks()
})

test.each([false,true])('portal detection preserves custom footer and respects fullPage=%s',async fullPage=>{
 const {portalFixture}=await import('./portal-chrome.test-fixture')
 const fixture=portalFixture()
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width:1400,height:39} as DOMRect)
 const root=document.createElement('div');fixture.main.append(root)
 const app=createApp({render:()=>h(AppShell,{wideLayout:'web',locale:'en',modelValue:'home',pages:[{id:'home',title:'Home'}],ctx:{fullPage,displayName:'Portal App',user:{name:'alice',isAdmin:false}}},{'sidebar-footer':()=>h('p','App footer')})})
 const remove=vi.spyOn(window,'removeEventListener')
 app.mount(root);await nextTick();await nextTick()
 expect(root.querySelector('.app-shell')?.classList.contains('app-shell-portal-brand')).toBe(fullPage)
 expect(root.textContent).toContain('App footer')
 app.unmount()
 if(fullPage) expect(remove.mock.calls.some(([event])=>event==='resize')).toBe(true)
 fixture.remove();vi.restoreAllMocks()
})

// While an App's first reads load, the sidebar and the page title already
// stand; the page body says it is loading and the App's actions wait.
test('a loading shell draws the sidebar and title with a loading body in place of the page',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const Page=()=>h('p',{class:'page'},'Page body')
 const app=createApp({data:()=>({loading:true}),render(){return h(AppShell,{appRoot:true,locale:'en',loading:this.loading,modelValue:'home',pages:[{id:'home',title:'Home',component:Page},{id:'logs',title:'Logs'}]},{default:()=>h('p',{class:'slot'},'Slot body'),actions:()=>h('button',{class:'act'},'New')})}})
 app.config.idPrefix='shell-loading';const vm=app.mount(root) as unknown as {loading:boolean};await nextTick()
 expect(root.querySelector('[data-ui-sidebar-body]')?.textContent).toContain('Logs')
 expect(root.querySelector('[data-ui-page-header]')?.textContent).toContain('Home')
 expect(root.querySelector('.app-page-content')?.textContent).toContain('Loading…')
 expect(root.querySelector('.page, .slot, .act')).toBeNull()
 expect(root.querySelector('.app-shell')?.classList.contains('app-shell-fill')).toBe(true)
 vm.loading=false;await nextTick()
 expect(root.querySelector('.page')?.textContent).toBe('Page body')
 expect(root.querySelector('.slot')).not.toBeNull()
 expect(root.querySelector('.act')).not.toBeNull()
 expect(root.textContent).not.toContain('Loading…')
 app.unmount();root.remove()
})
