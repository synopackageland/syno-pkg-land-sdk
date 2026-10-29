// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {createApp,h,nextTick,ref} from 'vue'
import AppShell from './AppShell.vue'

test.each([
 {width:1024,fullPage:false,navigation:undefined},
 {width:1400,fullPage:true,navigation:undefined},
 {width:1024,fullPage:false,navigation:'sidebar'},
 {width:1024,fullPage:false,navigation:'top'},
 {width:1400,fullPage:true,navigation:'top'},
 {width:390,fullPage:true,navigation:'top'},
] as const)('real navigation: $navigation at $width (fullPage=$fullPage)',async({width,fullPage,navigation})=>{
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width} as DOMRect)
 const root=document.createElement('div');document.body.append(root)
 const page=ref('home')
 const app=createApp({render:()=>h(AppShell,{
  locale:'en',modelValue:page.value,'onUpdate:modelValue':(value:string)=>{page.value=value},
  pages:[{id:'home',title:'Home'},{id:'logs',title:'Logs'}],navigation,
  ctx:{fullPage,displayName:'My App',user:{name:'alice',isAdmin:false}},
 })})
 try{
  app.mount(root);await nextTick();await nextTick()
  const layout=root.querySelector('.app-shell')!
  const mobile=width===390
  const top=navigation==='top'&&!mobile
  expect(layout.getAttribute('data-ui-navigation')).toBe(top?'top':null)
  expect(layout.getAttribute('data-ui-layout-mode')).toBe(mobile?'mobile':fullPage?'web':'window')
  expect(root.querySelector('nav')?.getAttribute('data-ui-navigation')).toBe(top?'top':null)
  expect(root.querySelector('[data-ui-mobile-menu]')!==null).toBe(mobile)
  if(top){
   if(fullPage) expect(root.querySelector('[data-ui-sidebar-footer]')?.textContent).toContain('alice')
   else expect(root.querySelector('[data-ui-sidebar-footer]')).toBeNull()
   expect(root.querySelector('[data-ui-sidebar-collapse], [data-ui-sidebar-resize]')).toBeNull()
  }
  if(mobile){
   root.querySelector<HTMLButtonElement>('[data-ui-mobile-menu]')!.click();await nextTick()
   expect(root.querySelector('[role="dialog"]')?.getAttribute('aria-hidden')).not.toBe('true')
  }
  const logs=Array.from(root.querySelectorAll<HTMLElement>('nav button')).find(button=>button.textContent?.includes('Logs'))!
  logs.click();await nextTick();await nextTick()
  expect(page.value).toBe('logs')
  expect(root.querySelector('[data-ui-page-header]')?.textContent).toContain('Logs')
  if(mobile) expect(root.querySelector('[role="dialog"]')?.getAttribute('aria-hidden')).toBe('true')
 }finally{app.unmount();root.remove();vi.restoreAllMocks()}
})

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

// Exercise the published UI brand event with the real shell, including App overrides.
test.each([1365,390])('the published UI brand selects home and allows an App override at %spx',async width=>{
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width} as DOMRect)
 const root=document.createElement('div');document.body.append(root)
 const page=ref('logs'),override=ref(false)
 const app=createApp({render:()=>h(AppShell,{
  appRoot:true,locale:'en',modelValue:page.value,'onUpdate:modelValue':(value:string)=>{page.value=value},
  pages:[{id:'home',title:'Home',hidden:true},{id:'logs',title:'Logs'},{id:'custom',title:'Custom home'}],
  ctx:{fullPage:true,displayName:'My App'},
  onBrand:(event:MouseEvent)=>{if(override.value){event.preventDefault();page.value='custom'}},
 })})
 try{
  app.mount(root);await nextTick();await nextTick()
  const selector=width===390?'[data-ui-mobile-navigation] [data-ui-brand-action]':'[data-ui-layout-sidebar] [data-ui-brand-action]'
  const brand=root.querySelector<HTMLButtonElement>(selector)!
  expect(brand).not.toBeNull()
  brand.click();await nextTick()
  expect(page.value).toBe('home')
  expect(root.querySelector('[data-ui-page-header]')?.textContent).toContain('Home')
  page.value='logs';override.value=true;await nextTick()
  brand.click();await nextTick()
  expect(page.value).toBe('custom')
  expect(root.querySelector('[data-ui-page-header]')?.textContent).toContain('Custom home')
 }finally{app.unmount();root.remove();vi.restoreAllMocks()}
})

// ui.banner: hidden leaves no DSM account menu: the shell's account signs out of DSM.
test.each([
 {host:'dsm',signOut:true,shown:true},
 {host:'standalone-web',signOut:true,shown:false},
 {host:'dsm',signOut:false,shown:false},
] as const)('a full page signs out from the sidebar account on $host (signOut=$signOut)',async({host,signOut,shown})=>{
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width:1400} as DOMRect)
 const root=document.createElement('div');document.body.append(root)
 const requested=vi.fn(()=>({status:'requested'} as const))
 const hostAdapter={platform:()=>({host}),...(signOut?{signOut:requested}:{})}
 const app=createApp({render:()=>h(AppShell,{locale:'en',modelValue:'home',pages:[{id:'home',title:'Home'}],
  ctx:{fullPage:true,displayName:'My App',user:{name:'alice',isAdmin:false},host:hostAdapter as never}})})
 try{
  app.mount(root);await nextTick();await nextTick()
  const footer=root.querySelector('[data-ui-sidebar-footer]')!
  expect(footer.textContent).toContain('alice')
  const button=Array.from(footer.querySelectorAll('button')).find(b=>b.textContent?.includes('Sign out'))
  expect(button!==undefined).toBe(shown)
  button?.click()
  expect(requested).toHaveBeenCalledTimes(shown?1:0)
 }finally{app.unmount();root.remove();vi.restoreAllMocks()}
})
