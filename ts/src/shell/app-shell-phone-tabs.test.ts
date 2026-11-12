// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick,ref} from 'vue'
import {Download,FileText,Home,List,Settings,Star} from '@lucide/vue'
import AppShell from './AppShell.vue'
import {phoneTabItems,sidebarGroups,type AppShellPage} from './app-pages'

afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals();document.body.innerHTML=''})
async function settle(){await nextTick();await nextTick();await nextTick()}
function viewport(width:number,coarse:boolean){
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width} as DOMRect)
 const media=new EventTarget() as MediaQueryList
 Object.assign(media,{matches:coarse,media:'(pointer: coarse)',addListener:()=>{},removeListener:()=>{}})
 vi.stubGlobal('matchMedia',vi.fn(()=>media))
}
function mount(props:{pages:readonly AppShellPage[];modelValue?:string;[key:string]:unknown},slots?:Record<string,unknown>){
 const page=ref(String(props.modelValue ?? 'home'))
 const updates:string[]=[]
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{appRoot:true,locale:'en',...props,modelValue:page.value,'onUpdate:modelValue':(key:string)=>{updates.push(key);page.value=key}},slots)})
 app.mount(root)
 return {root,page,updates,unmount:()=>app.unmount()}
}
const tabs=(root:Element)=>Array.from(root.querySelectorAll<HTMLButtonElement>('[data-ui-tab-bar-item]'))

const pages:AppShellPage[]=[
 {id:'home',title:'Home',icon:Home},
 {id:'settings',title:'Settings',icon:Settings,placement:'bottom'},
 {id:'logs',title:'Logs',icon:FileText,group:'Tools'},
 {id:'detail',title:'Detail',hidden:true},
 {id:'plain',title:'Plain'},
]

test('a phone shows the sidebar pages as icon-only tabs that follow and set the page',async()=>{
 viewport(390,true)
 const {root,page,updates,unmount}=mount({pages})
 try{
  await settle()
  const bar=root.querySelector('[data-ui-tab-bar]')
  expect(bar).not.toBeNull()
  // The sidebar's order and pages: groups first, bottom placement last, hidden left out.
  expect(tabs(root).map(tab=>tab.getAttribute('data-ui-tab-bar-item'))).toEqual(['home','plain','logs','settings'])
  expect(tabs(root).map(tab=>tab.textContent?.trim())).toEqual(['Home','Plain','Logs','Settings'])
  expect(root.querySelector('[data-ui-tab-bar-item="home"]')?.hasAttribute('data-icon-only')).toBe(true)
  // A page without an icon shows its label, as AppTabBar does.
  expect(root.querySelector('[data-ui-tab-bar-item="plain"]')?.hasAttribute('data-icon-only')).toBe(false)
  expect(root.querySelector('[data-ui-tab-bar-item="home"]')?.getAttribute('aria-current')).toBe('page')
  root.querySelector<HTMLButtonElement>('[data-ui-tab-bar-item="logs"]')!.click();await settle()
  expect(updates).toEqual(['logs'])
  expect(root.querySelector('[data-ui-tab-bar-item="logs"]')?.getAttribute('aria-current')).toBe('page')
  // A page outside the tabs marks none current.
  page.value='detail';await settle()
  expect(root.querySelector('[data-ui-tab-bar] [aria-current="page"]')).toBeNull()
  // The More tab opens the navigation drawer in place of the menu button.
  expect(root.querySelector('[data-ui-mobile-menu]')).toBeNull()
  root.querySelector<HTMLButtonElement>('[data-ui-tab-bar-more]')!.click();await settle()
  expect(document.querySelector('[data-ui-nav-drawer]')?.getAttribute('data-state')).toBe('open')
 }finally{unmount()}
})

test.each([
 {name:'desktop',width:1280,coarse:false,ctx:undefined},
 {name:'narrow desktop',width:390,coarse:false,ctx:undefined},
 {name:'DSM window',width:390,coarse:true,ctx:{fullPage:false}},
 {name:'wide full page',width:1280,coarse:true,ctx:{fullPage:true}},
])('no tab bar on a $name',async({width,coarse,ctx})=>{
 viewport(width,coarse)
 const {root,unmount}=mount({pages,ctx})
 try{
  await settle()
  expect(root.querySelector('[data-ui-tab-bar]')).toBeNull()
 }finally{unmount()}
})

test('a full page in the Portal on a phone shows the tab bar',async()=>{
 viewport(390,true)
 const {root,unmount}=mount({pages,ctx:{fullPage:true}})
 try{
  await settle()
  expect(tabs(root)).toHaveLength(4)
 }finally{unmount()}
})

test('more than the bar holds goes under More, which lists the rest and the drawer',async()=>{
 viewport(390,true)
 const many:AppShellPage[]=[Home,Download,List,Star,FileText,Settings].map((icon,index)=>({id:`p${index}`,title:`Page ${index}`,icon}))
 const {root,updates,unmount}=mount({pages:many,modelValue:'p0'})
 try{
  await settle()
  expect(tabs(root).map(tab=>tab.getAttribute('data-ui-tab-bar-item'))).toEqual(['p0','p1','p2','p3'])
  const more=root.querySelector<HTMLButtonElement>('[data-ui-tab-bar-more]')!
  expect(more).not.toBeNull()
  more.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,button:0}));more.click();await settle()
  const items=Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]'))
  expect(items.map(item=>item.textContent?.trim())).toEqual(['Page 4','Page 5','Application navigation'])
  items[1]!.click();await settle()
  expect(updates).toEqual(['p5'])
  expect(root.querySelector('[data-ui-tab-bar-more]')?.getAttribute('aria-current')).toBe('page')
 }finally{unmount()}
})

test("phoneTabs 'none' keeps the drawer as the navigation",async()=>{
 viewport(390,true)
 const {root,unmount}=mount({pages,phoneTabs:'none'})
 try{
  await settle()
  expect(root.querySelector('[data-ui-tab-bar]')).toBeNull()
  expect(root.querySelector('[data-ui-mobile-menu]')).not.toBeNull()
 }finally{unmount()}
})

test.each(['auto','none'] as const)("the App's tabbar slot wins over phoneTabs %s",async phoneTabs=>{
 viewport(390,true)
 const {root,unmount}=mount({pages,phoneTabs},{tabbar:()=>h('nav',{'data-own-tabs':''},'Own')})
 try{
  await settle()
  expect(root.querySelector('[data-own-tabs]')).not.toBeNull()
  expect(root.querySelector('[data-ui-tab-bar]')).toBeNull()
 }finally{unmount()}
})

test('one listed page needs no tab bar',async()=>{
 viewport(390,true)
 const {root,unmount}=mount({pages:[{id:'home',title:'Home',icon:Home},{id:'detail',title:'Detail',hidden:true}]})
 try{
  await settle()
  expect(root.querySelector('[data-ui-tab-bar]')).toBeNull()
 }finally{unmount()}
})

test('tab items take the sidebar items and keep counts, other badge text as a dot',()=>{
 const items=phoneTabItems(sidebarGroups([
  {id:'a',title:'A',icon:Home,badge:3},
  {id:'b',title:'B',badge:'12'},
  {id:'c',title:'C',badge:'New'},
  {id:'d',title:'D',badge:''},
  {id:'e',title:'E',hidden:true},
 ]))
 expect(items).toEqual([
  {key:'a',label:'A',icon:Home,badge:3},
  {key:'b',label:'B',icon:undefined,badge:12},
  {key:'c',label:'C',icon:undefined,badge:true},
  {key:'d',label:'D',icon:undefined,badge:undefined},
 ])
})
