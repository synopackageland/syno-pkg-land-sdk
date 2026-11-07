// @vitest-environment jsdom
// One top bar on a phone in DSM's Application Portal: DSM's header row gives
// way to the App's bar (collapsePortalHeader), a tab page draws no title row,
// and the account joins the App's bar. Fake DSM 7.3 markup, as a phone loads it.
import {afterEach,beforeEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick,ref} from 'vue'
import {AppTabBar,PageHeader} from '@synopackageland/ui'
import AppShell from './AppShell.vue'
import {collapsePortalHeader,portalBodyHeight,portalCollapsedAttr,portalHeaderHeight,portalMobileAttr} from './portal-chrome'

// The menu's floating layer watches its anchor.
beforeEach(()=>{vi.stubGlobal('IntersectionObserver',class{observe(){} unobserve(){} disconnect(){} takeRecords(){return []}})})
afterEach(()=>{document.body.replaceChildren();vi.restoreAllMocks();vi.unstubAllGlobals()})
async function settle(){for(let i=0;i<6;i++)await nextTick()}
function dsmWindow({account=true}={}){
 const main=document.createElement('div')
 main.className='x-window sds-standalone-main-window sds-window-v5 x-window-maximized'
 main.innerHTML='<div class="x-window-tl"><div class="x-window-tr"><div class="x-window-tc"><div class="sds-standalone-main-window-header"></div><div class="x-window-header x-panel-icon"><div class="sds-standalone-main-window-header-text"><span class="x-window-header-text">Meeting Notes</span></div></div></div></div></div><div class="x-window-bwrap"><div class="x-window-ml"><div class="x-window-mr"><div class="x-window-mc"><div class="x-window-body" style="width: 390px; height: 804px;"><div class="dsmapp-vue-root"></div></div></div></div></div></div>'
 document.body.append(main)
 const tl=main.querySelector<HTMLElement>('.x-window-tl')!,body=main.querySelector<HTMLElement>('.x-window-body')!,app=main.querySelector<HTMLElement>('.dsmapp-vue-root')!
 Object.defineProperty(tl,'offsetHeight',{configurable:true,get:()=>40})
 const addAccount=()=>{main.querySelector('.sds-standalone-main-window-header')!.innerHTML='<span class="x-btn sds-standalone-main-window-header__toolbar__btn"><em class="x-btn-arrow"><button type="button" class="x-btn-text">alice</button></em></span>'}
 if(account)addAccount()
 return {main,tl,body,app,addAccount}
}
function viewport(width:number,coarse=true){
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockImplementation(()=>({width,height:40,top:0,left:0,right:width,bottom:40,x:0,y:0,toJSON(){}}) as DOMRect)
 const media=new EventTarget() as MediaQueryList
 Object.assign(media,{matches:coarse,media:'(pointer: coarse)',addListener:()=>{},removeListener:()=>{}})
 vi.stubGlobal('matchMedia',vi.fn(()=>media))
 return (matches:boolean)=>{Object.assign(media,{matches});media.dispatchEvent(Object.assign(new Event('change'),{matches}))}
}

test('collapsePortalHeader moves the header row out and the body reaches the bottom; false and undo restore',()=>{
 const f=dsmWindow()
 Object.defineProperty(window,'innerHeight',{configurable:true,value:844})
 const undo=collapsePortalHeader(f.app,'bar')
 expect(f.main.getAttribute(portalCollapsedAttr)).toBe('bar')
 expect(f.main.style.getPropertyValue(portalHeaderHeight)).toBe('40px')
 expect(f.main.style.getPropertyValue(portalBodyHeight)).toBe('844px')
 // Rotation: the Portal's window follows the viewport, whatever DSM writes inline meanwhile.
 Object.defineProperty(window,'innerHeight',{configurable:true,value:390});f.body.style.height='380px'
 collapsePortalHeader(f.app,'scroll')
 expect(f.main.getAttribute(portalCollapsedAttr)).toBe('scroll')
 expect(f.main.style.getPropertyValue(portalBodyHeight)).toBe('390px')
 expect(f.body.style.height).toBe('380px')
 collapsePortalHeader(f.app,false)
 expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
 expect(f.main.getAttribute('style')??'').not.toContain('--synopkgland')
 // DSM sized the body during the rotation with the header moved: restore gives it the viewport less the header.
 expect(f.body.style.height).toBe('350px')
 // A window that is not maximized: DSM's inline height and the header's.
 f.main.classList.remove('x-window-maximized');f.body.style.height='804px'
 collapsePortalHeader(f.app,'bar')
 expect(f.main.style.getPropertyValue(portalBodyHeight)).toBe('844px')
 undo()
 expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
})
test('no banner, a root outside the window body or a detached root collapse nothing',()=>{
 const f=dsmWindow()
 f.main.classList.add('sds-standalone-main-window--no-banner')
 collapsePortalHeader(f.app,'bar')
 expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
 f.main.classList.remove('sds-standalone-main-window--no-banner')
 const outside=document.createElement('div');f.main.querySelector('.x-window-tc')!.append(outside)
 collapsePortalHeader(outside,'bar')
 expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
 collapsePortalHeader(document.createElement('div'),'bar')
 expect(document.querySelector(`[${portalCollapsedAttr}]`)).toBeNull()
})

const pages=[{id:'home',title:'Home'},{id:'meetings',title:'Meetings'},{id:'about',title:'About'}]
const tabs=pages.slice(0,2).map(p=>({key:p.id,label:p.title}))
function mountShell(target:HTMLElement,{fullPage=true,user='alice',actions=[] as {key:string;label:string;primary?:boolean}[]}={}){
 const page=ref('home'),detail=ref(false),signOut=vi.fn(),selected=vi.fn()
 const app=createApp({render:()=>h(AppShell,{
  appRoot:true,locale:'en',pages,modelValue:page.value,'onUpdate:modelValue':(v:string)=>{page.value=v},
  headerActions:actions,onHeaderAction:selected,
  ctx:{fullPage,displayName:'Meeting Notes',...(user?{user:{name:user,isAdmin:true}}:{}),host:{signOut,platform:()=>({host:'dsm',dsmStandalone:fullPage})} as never},
 },{
  tabbar:({page:current}:{page:string})=>h(AppTabBar,{items:tabs,modelValue:current,'onUpdate:modelValue':(v:string)=>{page.value=v}}),
  default:()=>detail.value?h(PageHeader,{title:'Weekly sync',back:true,onBack:()=>{detail.value=false}}):h('p','List'),
 })})
 app.mount(target)
 return {app,page,detail,signOut,selected,root:target}
}
const bar=(root:ParentNode)=>root.querySelector('[data-ui-mobile-navigation]:not([hidden])')

test('a phone tab page in the portal: DSM\'s compact row is the one bar; a sub-page\'s bar replaces it with the account',async()=>{
 viewport(390)
 const f=dsmWindow(),s=mountShell(f.app)
 try{
  await settle()
  expect(f.main.hasAttribute(portalMobileAttr)).toBe(true)
  expect(bar(f.app)).toBeNull()
  expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
  expect(f.app.querySelector('[data-ui-mobile-title]')).toBeNull()
  // A page reached from More keeps its title.
  s.page.value='about';await settle()
  expect(bar(f.app)?.querySelector('[data-ui-mobile-title]')?.textContent).toBe('About')
  expect(f.main.getAttribute(portalCollapsedAttr)).toBe('bar')
  s.page.value='meetings';await settle()
  expect(bar(f.app)).toBeNull()
  expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
  // Drill down: Back, the title and the account, in place of DSM's row.
  s.detail.value=true;await settle()
  expect(bar(f.app)?.querySelector('[data-ui-mobile-back]')).not.toBeNull()
  expect(bar(f.app)?.querySelector('[data-ui-mobile-title]')?.textContent).toBe('Weekly sync')
  expect(f.main.getAttribute(portalCollapsedAttr)).toBe('bar')
  const account=bar(f.app)!.querySelector<HTMLButtonElement>('[data-ui-mobile-action]')!
  expect(account.getAttribute('aria-label')).toBe('alice')
  account.click();await settle()
  const dialog=document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).toContain('alice')
  Array.from(dialog.querySelectorAll('button')).find(b=>b.textContent?.includes('Sign out'))!.click()
  expect(s.signOut).toHaveBeenCalledOnce()
  s.detail.value=false;await settle()
  expect(bar(f.app)).toBeNull()
  expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
 }finally{s.app.unmount()}
 // Unmounting restores DSM's header.
 expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
 expect(f.main.hasAttribute(portalMobileAttr)).toBe(false)
})

test('App header actions in the portal: one bar, the actions and the account after them in its one "…"',async()=>{
 viewport(390)
 const f=dsmWindow(),s=mountShell(f.app,{actions:[{key:'share',label:'Share'},{key:'export',label:'Export'}]})
 try{
  await settle()
  expect(bar(f.app)).not.toBeNull()
  expect(f.main.getAttribute(portalCollapsedAttr)).toBe('bar')
  // A tab page's bar draws no title or brand; screen readers keep the page title.
  expect(bar(f.app)!.getAttribute('data-ui-mobile-bar-title')).toBe('none')
  expect(bar(f.app)!.querySelector('[data-ui-mobile-title], [data-ui-mobile-app-icon], [data-ui-sidebar-brand]')).toBeNull()
  expect(bar(f.app)!.querySelector('[data-ui-mobile-title-hidden]')?.textContent).toBe('Home')
  // No entry is a bar button for being first: the bar shows one "…" only.
  expect(bar(f.app)!.querySelectorAll('[data-ui-mobile-more], [data-ui-mobile-action]')).toHaveLength(1)
  bar(f.app)!.querySelector<HTMLButtonElement>('[data-ui-mobile-more]')!.click();await settle()
  const items=Array.from(document.querySelectorAll('[role="menuitem"]')).map(item=>item.textContent?.trim())
  expect(items).toEqual(['Share','Export','alice'])
 }finally{s.app.unmount()}
})

test('a header action marked primary is the bar button; the rest and the account stay in "…"',async()=>{
 viewport(390)
 const f=dsmWindow(),s=mountShell(f.app,{actions:[{key:'export',label:'Export'},{key:'share',label:'Share',primary:true}]})
 try{
  await settle()
  bar(f.app)!.querySelector<HTMLButtonElement>('[data-ui-mobile-action]')!.click()
  expect(s.selected).toHaveBeenCalledWith('share')
  bar(f.app)!.querySelector<HTMLButtonElement>('[data-ui-mobile-more]')!.click();await settle()
  const items=Array.from(document.querySelectorAll('[role="menuitem"]')).map(item=>item.textContent?.trim())
  expect(items).toEqual(['Export','alice'])
  expect(bar(f.app)!.querySelectorAll('[data-ui-mobile-more], [data-ui-mobile-action]')).toHaveLength(2)
 }finally{s.app.unmount()}
})

test('DSM builds its header late, and mobile ↔ wide restores it',async()=>{
 const setCoarse=viewport(390)
 const f=dsmWindow({account:false}),s=mountShell(f.app)
 try{
  await settle()
  // No DSM account yet: the account is in the App's bar, DSM's row gives way.
  expect(bar(f.app)?.querySelector('[data-ui-mobile-action]')?.getAttribute('aria-label')).toBe('alice')
  expect(f.main.getAttribute(portalCollapsedAttr)).toBe('bar')
  f.addAccount();await settle()
  expect(bar(f.app)).toBeNull()
  expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
  // A mouse (not a phone): DSM's header stays; AppLayout keeps its narrow bar.
  s.detail.value=true;await settle()
  expect(f.main.getAttribute(portalCollapsedAttr)).toBe('bar')
  setCoarse(false);await settle()
  expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
  setCoarse(true);await settle()
  expect(f.main.getAttribute(portalCollapsedAttr)).toBe('bar')
 }finally{s.app.unmount()}
 expect(f.main.hasAttribute(portalCollapsedAttr)).toBe(false)
})

test('outside the portal a tab page draws a bar only for the account or actions; a DSM window keeps its title row',async()=>{
 viewport(390)
 for(const [options,expected] of [
  [{user:''},'none'],
  [{user:'alice'},'alice'],
  [{user:'',fullPage:false},'Home'],
 ] as const){
  const root=document.createElement('div');document.body.append(root)
  const s=mountShell(root,options)
  try{
   await settle()
   if(expected==='none')expect(bar(root)).toBeNull()
   else if(expected==='alice')expect(bar(root)?.querySelector('[data-ui-mobile-action]')?.getAttribute('aria-label')).toBe('alice')
   else expect(bar(root)?.querySelector('[data-ui-mobile-title]')?.textContent).toBe('Home')
  }finally{s.app.unmount();root.remove()}
 }
})

test('a page outside the tab bar keeps its title bar and Back while the App marks its parent tab current',async()=>{
 viewport(390)
 const root=document.createElement('div');document.body.append(root)
 // 'live' is hidden (not in the sidebar) and not a tab; the App keeps Home's tab lit on it.
 const shown=[...pages,{id:'live',title:'Live',hidden:true}]
 const page=ref('home'),back=ref(false)
 const app=createApp({render:()=>h(AppShell,{
  appRoot:true,locale:'en',pages:shown,modelValue:page.value,'onUpdate:modelValue':(v:string)=>{page.value=v},
  ctx:{fullPage:true,displayName:'Meeting Notes',host:{platform:()=>({host:'dsm',dsmStandalone:true})} as never},
 },{
  tabbar:({page:current}:{page:string})=>h(AppTabBar,{items:tabs,modelValue:current==='live'?'home':current,'onUpdate:modelValue':(v:string)=>{page.value=v}}),
  default:({page:current}:{page:string})=>current==='live'&&back.value?h(PageHeader,{title:'Live',back:true,onBack:()=>{page.value='home'}}):h('p',current),
 })})
 try{
  app.mount(root);await settle()
  // The tab page: no title row and, with nothing for it, no bar.
  expect(bar(root)).toBeNull()
  page.value='live';await settle()
  expect(root.querySelector('[data-ui-tab-bar-item="home"]')?.getAttribute('aria-current')).toBe('page')
  expect(bar(root)?.querySelector('[data-ui-mobile-title]')?.textContent).toBe('Live')
  back.value=true;await settle()
  expect(bar(root)?.querySelector('[data-ui-mobile-title]')?.textContent).toBe('Live')
  bar(root)!.querySelector<HTMLButtonElement>('[data-ui-mobile-back]')!.click();await settle()
  expect(page.value).toBe('home')
  expect(bar(root)).toBeNull()
 }finally{app.unmount();root.remove()}
})

test.each([
 {width:1280,coarse:false,place:'notices'},
 {width:390,coarse:true,place:'bottom'},
])('accessoryDesktop="notices" draws the accessory at the top off a phone ($width)',async({width,coarse,place})=>{
 viewport(width,coarse)
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{appRoot:true,locale:'en',pages,modelValue:'home',accessoryDesktop:'notices'},{
  default:()=>h('p','Body'),
  accessory:({phone}:{phone:boolean})=>h('button',{'data-task':''},`task ${phone}`),
 })})
 try{
  app.mount(root);await settle()
  expect(root.querySelectorAll('[data-task]')).toHaveLength(1)
  const task=root.querySelector('[data-task]')!
  expect(!!task.closest('.app-notices')).toBe(place==='notices')
  expect(!!task.closest('[data-ui-bottom-accessory]')).toBe(place==='bottom')
  expect(root.querySelector('[data-ui-bottom-accessory]')===null).toBe(place==='notices')
 }finally{app.unmount();root.remove()}
})
