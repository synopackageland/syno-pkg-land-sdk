// @vitest-environment jsdom
import {afterEach,expect,expectTypeOf,test,vi} from 'vitest'
import {createApp,h,nextTick,ref} from 'vue'
import {AppRoot,AppTabBar,type DropdownMenuEntry} from '@synopackageland/ui'
import AppShell from './AppShell.vue'

const pages=[{id:'home',title:'Home'},{id:'logs',title:'Logs'}]
const items=pages.map(page=>({key:page.id,label:page.title}))
afterEach(()=>vi.restoreAllMocks())
async function settle(){await nextTick();await nextTick();await nextTick()}
function viewport(width:number,coarse=false){
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width} as DOMRect)
 const media=new EventTarget() as MediaQueryList
 Object.assign(media,{matches:coarse,media:'(pointer: coarse)',addListener:()=>{},removeListener:()=>{}})
 vi.stubGlobal('matchMedia',vi.fn(()=>media))
 return (matches:boolean)=>{Object.assign(media,{matches});media.dispatchEvent(Object.assign(new Event('change'),{matches}))}
}

test.each([false,true])('the forwarded tabbar owns phone navigation, the default replaces it and none restores the menu (appRoot=%s)',async appRoot=>{
 viewport(390,true)
 const page=ref('home'),tabbar=ref(true),more=ref(true),phoneTabs=ref<'auto'|'none'>('auto')
 const root=document.createElement('div');document.body.append(root)
 const render=()=>h(AppShell,{appRoot,locale:'en',pages,phoneTabs:phoneTabs.value,modelValue:page.value,'onUpdate:modelValue':(key:string)=>{page.value=key}},tabbar.value?{
  tabbar:({page:current,phone}:{page:string;phone:boolean})=>h(AppTabBar,{items,modelValue:current,more:more.value,ariaLabel:`Phone ${phone}`,'onUpdate:modelValue':(key:string)=>{page.value=key}}),
 }:undefined)
 const app=createApp({render:()=>appRoot?render():h(AppRoot,null,{default:render})})
 try{
  app.mount(root);await settle()
  expect(root.querySelector('[data-ui-tab-bar]')?.getAttribute('aria-label')).toBe('Phone true')
  expect(root.querySelector('[data-ui-mobile-menu]')).toBeNull()
  root.querySelector<HTMLButtonElement>('[data-ui-tab-bar-item="logs"]')!.click();await settle()
  expect(page.value).toBe('logs')
  expect(root.querySelector('[data-ui-tab-bar-item="logs"]')?.getAttribute('aria-current')).toBe('page')
  root.querySelector<HTMLButtonElement>('[data-ui-tab-bar-more]')!.click();await settle()
  expect(document.querySelector('[data-ui-nav-drawer]')?.getAttribute('data-state')).toBe('open')
  document.querySelector<HTMLButtonElement>('[data-ui-nav-drawer] button[aria-label="Close"]')?.click();await settle()
  more.value=false;await settle()
  expect(root.querySelector('[data-ui-tab-bar-more]')).toBeNull()
  expect(root.querySelector('[data-ui-mobile-menu]')).toBeNull()
  tabbar.value=false;await settle()
  expect(root.querySelector('[data-ui-tab-bar]')?.getAttribute('aria-label')).toBe('Quick navigation')
  expect(root.querySelector('[data-ui-mobile-menu]')).toBeNull()
  phoneTabs.value='none';await settle()
  expect(root.querySelector('[data-ui-tab-bar]')).toBeNull()
  expect(root.querySelector('[data-ui-mobile-menu]')).not.toBeNull()
 }finally{app.unmount();root.remove();vi.unstubAllGlobals()}
})

test('phone header title and actions update without changing the page title or page actions',async()=>{
 viewport(390)
 const title=ref<string|undefined>('Details'),actions=ref<DropdownMenuEntry[]>([{key:'save',label:'Save'},{key:'export',label:'Export'}])
 const selected=vi.fn(),root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{appRoot:true,locale:'en',pages,modelValue:'home',title:title.value,headerActions:actions.value,onHeaderAction:selected},{actions:()=>h('button',{'data-page-action':''},'Refresh')})})
 try{
  app.mount(root);await settle()
  expect(root.querySelector('[data-ui-mobile-title]')?.textContent).toBe('Details')
  expect(root.querySelector('h1.app-page-title')?.textContent).toBe('Home')
  expect(root.querySelector('[data-page-action]')?.textContent).toBe('Refresh')
  // With page actions in the bar, the UI places header actions in More.
  const directAction=!!root.querySelector('[data-ui-mobile-action]')
  const selectAction=async(label:string)=>{
   const primary=root.querySelector<HTMLButtonElement>('[data-ui-mobile-action]')
   if(primary){primary.click();return}
   root.querySelector<HTMLButtonElement>('[data-ui-mobile-more]')!.click();await settle()
   const item=Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find(item=>item.textContent?.trim()===label)
   expect(item).toBeDefined();item!.click();await settle()
  }
  await selectAction('Save')
  expect(selected).toHaveBeenCalledExactlyOnceWith('save')
  expect(root.querySelector('[data-ui-mobile-more]')).not.toBeNull()
  title.value=undefined;actions.value=[{key:'retry',label:'Retry'}];await settle()
  expect(root.querySelector('[data-ui-mobile-title]')?.textContent).toBe('Home')
  await selectAction('Retry')
  expect(selected).toHaveBeenLastCalledWith('retry')
  expect(!!root.querySelector('[data-ui-mobile-more]')).toBe(!directAction)
 }finally{app.unmount();root.remove();vi.unstubAllGlobals()}
})

test.each([
 {width:390,coarse:true,fullPage:true,phone:true},
 {width:390,coarse:false,fullPage:false,phone:false},
 {width:1280,coarse:true,fullPage:true,phone:false},
 {width:1280,coarse:false,fullPage:false,phone:false},
])('slot phone signal at $width, coarse=$coarse, fullPage=$fullPage',async({width,coarse,fullPage,phone})=>{
 const setCoarse=viewport(width,coarse)
 const scopes:Record<string,{page:string;phone:boolean}>={}
 const root=document.createElement('div');document.body.append(root)
 const slots=Object.fromEntries(['default','actions','meta','toolbar','notices','tabbar'].map(name=>[name,(scope:{page:string;phone:boolean})=>{scopes[name]={...scope};return h('span',name)}]))
 const app=createApp({render:()=>h(AppShell,{appRoot:true,locale:'en',pages,modelValue:'home',ctx:{fullPage}},slots)})
 try{
  app.mount(root);await settle()
  for(const name of ['default','actions','meta','toolbar','notices',...(width===390?['tabbar']:[])])expect(scopes[name]).toEqual({page:'home',phone})
  if(width===1280){
   expect(scopes.tabbar).toBeUndefined()
   expect(root.querySelector('[data-ui-mobile-navigation], [data-ui-tab-bar]')).toBeNull()
   expect(root.querySelector('[data-ui-layout-mode]')?.getAttribute('data-ui-layout-mode')).toBe(fullPage?'web':'window')
  }else{
   setCoarse(!coarse);await settle()
   expect(scopes.default.phone).toBe(!coarse)
  }
 }finally{app.unmount();root.remove();vi.unstubAllGlobals()}
})

test('public shell types expose the UI header contract and phone slot scope',()=>{
 type Shell=InstanceType<typeof AppShell>
 expectTypeOf<Shell['$props']['title']>().toEqualTypeOf<string|undefined>()
 expectTypeOf<Shell['$props']['headerActions']>().toEqualTypeOf<DropdownMenuEntry[]|undefined>()
 expectTypeOf<Parameters<NonNullable<Shell['$slots']['tabbar']>>[0]>().toEqualTypeOf<{page:string;phone:boolean}>()
 expectTypeOf<Parameters<NonNullable<Shell['$slots']['accessory']>>[0]>().toEqualTypeOf<{page:string;phone:boolean}>()
})

test.each([390,1280])('the accessory stays outside the page, follows navigation and draws nothing when empty at %s',async width=>{
 viewport(width,true)
 const page=ref('home'),running=ref(true),supplied=ref(true),denied=ref(false)
 const root=document.createElement('div');document.body.append(root)
 const accessory=({page,phone}:{page:string;phone:boolean})=>running.value?h('button',{'data-running-task':''},`${page}:${phone}`):null
 const app=createApp({render:()=>h(AppShell,{appRoot:true,locale:'en',pages,modelValue:page.value,denied:denied.value},{
  default:()=>h('p','Page body'),
  ...(supplied.value?{accessory}:{}),
 })})
 const bar=()=>root.querySelector('[data-ui-bottom-accessory]')
 try{
  app.mount(root);await settle()
  expect(bar()?.textContent).toBe(`home:${width===390}`)
  expect(bar()?.closest('.app-page')).toBeNull()
  expect(root.querySelectorAll('[data-running-task]')).toHaveLength(1)
  const task=root.querySelector('[data-running-task]')
  page.value='logs';await settle()
  expect(bar()?.textContent).toBe(`logs:${width===390}`)
  expect(root.querySelector('[data-running-task]')).toBe(task)
  running.value=false;await settle()
  expect(bar()?.children).toHaveLength(0)
  expect(bar()?.textContent?.trim()).toBe('')
  running.value=true;await settle()
  expect(bar()).not.toBeNull()
  supplied.value=false;await settle()
  expect(bar()).toBeNull()
  supplied.value=true;await settle()
  denied.value=true;await settle()
  expect(bar()).toBeNull()
 }finally{app.unmount();root.remove();vi.unstubAllGlobals()}
})

test.each([false,true])('footer and denied scopes read the app-local phone signal (denied=%s)',async denied=>{
 viewport(390,true)
 const root=document.createElement('div');document.body.append(root)
 const name=denied?'denied':'sidebar-footer'
 const app=createApp({render:()=>h(AppShell,{appRoot:true,locale:'en',pages,phoneTabs:'none',modelValue:'home',denied},{[name]:({phone}:{phone:boolean})=>h('span',{'data-phone-slot':name},String(phone))})})
 try{
  app.mount(root);await settle()
  if(!denied){root.querySelector<HTMLButtonElement>('[data-ui-mobile-menu]')!.click();await settle()}
  expect(document.querySelector('[data-phone-slot]')?.textContent).toBe('true')
 }finally{app.unmount();root.remove();vi.unstubAllGlobals()}
})
