// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick,ref} from 'vue'
import {AsidePanel} from '@synopackageland/ui'
import {Home,Settings} from '@lucide/vue'
import AppShell from './AppShell.vue'
import type {AppShellPage} from './app-pages'

afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals();document.body.innerHTML=''})
async function settle(){for(let i=0;i<4;i++){await nextTick();await new Promise(r=>setTimeout(r,0))}}
function viewport(width:number){
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width} as DOMRect)
 const media=new EventTarget() as MediaQueryList
 Object.assign(media,{matches:false,media:'',addListener:()=>{},removeListener:()=>{}})
 vi.stubGlobal('matchMedia',vi.fn(()=>media))
}
const pages:AppShellPage[]=[{id:'home',title:'Home',icon:Home},{id:'settings',title:'Settings',icon:Settings}]
function mount(props:Record<string,unknown>={}){
 const page=ref('home'),state=ref<'open'|'collapsed'|'expanded'>('collapsed'),width=ref(400)
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{
  locale:'en',pages,modelValue:page.value,'onUpdate:modelValue':(v:string)=>{page.value=v},
  aside:state.value,'onUpdate:aside':(v:typeof state.value)=>{state.value=v},
  asideWidth:width.value,'onUpdate:asideWidth':(v:number)=>{width.value=v},...props,
 },{
  default:()=>h('p','Page'),
  aside:()=>h(AsidePanel,{title:'Assistant'},{default:()=>h('div',{'data-thread':''},'Thread')}),
 })})
 app.mount(root)
 return {root,page,state,width,unmount:()=>app.unmount()}
}

test('the aside slot reaches AppLayout on every page, and v-model:aside follows its rail',async()=>{
 viewport(1280)
 const {root,page,state,unmount}=mount()
 try{
  await settle()
  expect(root.querySelector('[data-ui-layout-aside]')).not.toBeNull()
  expect(root.querySelector('[data-ui-layout-aside]')?.getAttribute('data-ui-aside-state')).toBe('collapsed')
  root.querySelector<HTMLButtonElement>('[data-ui-aside-open]')!.click();await settle()
  expect(state.value).toBe('open')
  page.value='settings';await settle()
  expect(root.querySelector('[data-thread]')?.textContent).toBe('Thread')
  expect(root.querySelector('[data-ui-layout-aside]')?.getAttribute('data-ui-aside-state')).toBe('open')
 }finally{unmount()}
})

test("asideDock 'wide' docks it in a wide DSM window with the width from v-model",async()=>{
 viewport(1280)
 const {root,state,unmount}=mount({asideDock:'wide'})
 try{
  state.value='open';await settle()
  expect(root.querySelector('[data-ui-layout-mode]')?.getAttribute('data-ui-layout-mode')).toBe('window')
  expect(root.querySelector<HTMLElement>('[data-ui-aside-panel-root]')?.dataset.uiAsideMode).toBe('docked')
  expect(root.querySelector<HTMLElement>('[data-ui-layout-aside]')?.style.width).toBe('400px')
  expect(root.querySelector('[data-ui-aside-resize]')).not.toBeNull()
 }finally{unmount()}
})

test('without an aside slot the layout has no aside region',async()=>{
 viewport(1280)
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{locale:'en',pages,modelValue:'home'},{default:()=>h('p','Page')})})
 app.mount(root)
 try{await settle();expect(root.querySelector('[data-ui-layout-aside]')).toBeNull()}finally{app.unmount()}
})
