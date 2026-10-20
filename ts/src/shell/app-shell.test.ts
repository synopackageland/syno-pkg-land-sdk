// @vitest-environment jsdom
import {afterEach,expect,expectTypeOf,test,vi} from 'vitest'
import {Fragment,createApp,createCommentVNode,createTextVNode,defineComponent,h,nextTick,ref,type Slots} from 'vue'
import AppShell from './AppShell.vue'
import {appRefusal} from '../authorization/refusal'
import type {Context,HostAdapter,HostPlatformSnapshot,ShellContext} from '../context'
import type {AppShellPageSurface} from '../vue'

// The shell's AppLayout, recorded: its layout props and the sidebar footer it was given.
const seen=vi.hoisted(()=>({layout:{} as Record<string,unknown>,footer:false,viewport:0,restored:0,groups:[] as unknown[],width:undefined as unknown,surface:undefined as unknown,root:undefined as Record<string,unknown>|undefined,config:undefined as unknown,activate:undefined as (()=>void)|undefined}))
vi.mock('@synopackageland/ui',async importOriginal=>{
 const ui=await importOriginal<typeof import('@synopackageland/ui')>()
 const {defineComponent,h}=await import('vue')
 return {...ui,
  ensureDeviceViewport:()=>{seen.viewport++;return ()=>{seen.restored++}},
  AppLayout:defineComponent({props:['wideLayout','sidebarStorageKey','brandName','brandIcon','minHeight'],setup(props,{slots}:{slots:Slots}){
   return ()=>{
    seen.layout={...props};seen.footer=!!slots['sidebar-footer']
    return h('section',[slots.sidebar?.(),slots['sidebar-footer']&&h('footer',slots['sidebar-footer']()),slots.toolbar&&h('header',slots.toolbar()),slots.default?.()])
   }
  }}),
  AppRoot:defineComponent({props:['modalScope','dialogBounds'],emits:['activate'],setup(props,{slots,emit,attrs}){
   return ()=>{seen.root={...props,style:attrs.style};seen.activate=()=>emit('activate');return h('div',{'data-app-root':''},slots.default?.())}
  }}),
  ConfigProvider:defineComponent({props:['locale'],setup(props,{slots}){return ()=>{seen.config=props.locale;return h('div',{'data-config':''},slots.default?.())}}}),
  PageContent:defineComponent({props:['width','surface'],setup(props,{slots}){return ()=>{seen.width=props.width;seen.surface=props.surface;return h('div',slots.default?.())}}}),
  AppSidebar:defineComponent({props:['modelValue','groups'],emits:['update:modelValue'],setup(props,{emit}){
   return ()=>{
    seen.groups=props.groups
    return h('nav',(props.groups as {items:{key:string}[]}[]).flatMap(group=>group.items).map(item=>h('button',{'data-key':item.key,onClick:()=>emit('update:modelValue',item.key)},item.key)))
   }
  }}),
 }
})
afterEach(()=>{Object.assign(seen,{layout:{},footer:false,viewport:0,restored:0,groups:[],width:undefined,surface:undefined,root:undefined,config:undefined,activate:undefined})})

function ctx(fields:Partial<Context>,platform:Partial<HostPlatformSnapshot>={host:'dsm',dsmStandalone:false}):Context{
 const host={platform:()=>({web:true,host:'dsm',dsmStandalone:false,dsmLanguage:null,appLocale:null,systemVersion:null,fullVersion:null,theme:null,...platform})} as unknown as HostAdapter
 return {call:async()=>undefined as never,reopen:()=>()=>{},locale:'en',host,...fields}
}
const brand={displayName:'Hello',appIcon:'webman/3rdparty/Hello/images/icon_64.png',user:{name:'alice',isAdmin:false}}
async function mount(props:Record<string,unknown>,slots:Record<string,(scope:{page:string})=>unknown>={}){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{locale:'en',modelValue:'status',pages:['status'],...props},slots)})
 app.mount(root);await nextTick()
 return {root,unmount(){app.unmount();root.remove()}}
}

// An App lives inside one DSM window; the shell's wide layout follows that window, not the viewport.
test('a shell without a context lays out against the DSM window as before',async()=>{
 const shell=await mount({})
 expect(seen.layout).toMatchObject({wideLayout:'window',brandName:undefined,brandIcon:undefined,sidebarStorageKey:undefined})
 expect(seen.footer).toBe(false)
 shell.unmount()
 expect(seen.viewport).toBe(0)
})

// The window's title bar names the App; narrowed to the mobile layout, the phone bar does,
// with the App's name and icon rather than the DSM page's document.title.
test('a DSM desktop window keeps the window layout and no viewport, and names the App',async()=>{
 const shell=await mount({ctx:ctx(brand)})
 expect(seen.layout).toMatchObject({wideLayout:'window',brandName:'Hello',brandIcon:'webman/3rdparty/Hello/images/icon_64.png'})
 expect(seen.footer).toBe(false)
 shell.unmount()
 expect(seen.viewport).toBe(0)
})

test('a full page takes the web layout, names the App and shows the account',async()=>{
 const shell=await mount({ctx:ctx({...brand,fullPage:true})})
 expect(seen.layout).toMatchObject({wideLayout:'web',brandName:'Hello',brandIcon:'webman/3rdparty/Hello/images/icon_64.png'})
 expect(shell.root.querySelector('footer')?.textContent).toContain('alice')
 shell.unmount()
})

// Without one a phone lays a full page out ~980px wide and never reaches the mobile layout.
test('a full page adds a device-width viewport until it unmounts',async()=>{
 const shell=await mount({ctx:ctx({},{host:'standalone-web',dsmStandalone:null})})
 expect(seen).toMatchObject({viewport:1,restored:0})
 shell.unmount()
 expect(seen).toMatchObject({viewport:1,restored:1})
})

test('a full page without a signed-in user has no sidebar footer',async()=>{
 const shell=await mount({ctx:ctx({fullPage:true})})
 expect(seen.footer).toBe(false)
 shell.unmount()
})

test('a full page whose account has no name has no sidebar footer',async()=>{
 const shell=await mount({ctx:ctx({fullPage:true,user:{name:'',isAdmin:true}})})
 expect(seen.footer).toBe(false)
 shell.unmount()
})

test("the App's props and sidebar footer win over the context",async()=>{
 const shell=await mount({ctx:ctx({...brand,fullPage:true}),brandName:'Own',brandIcon:'own.png',wideLayout:'window',sidebarStorageKey:'Hello'},{'sidebar-footer':()=>'own footer'})
 expect(seen.layout).toMatchObject({wideLayout:'window',brandName:'Own',brandIcon:'own.png',sidebarStorageKey:'Hello'})
 expect(shell.root.querySelector('footer')?.textContent).toBe('own footer')
 shell.unmount()
})

test('a DSM window takes a sidebar footer the App gives',async()=>{
 const shell=await mount({ctx:ctx(brand)},{'sidebar-footer':()=>'own footer'})
 expect(shell.root.querySelector('footer')?.textContent).toBe('own footer')
 shell.unmount()
})

const Plain=defineComponent({props:['label'],setup:props=>()=>h('p',{class:'page-body'},props.label)})
const tunnel={id:'tunnel',title:'Tunnel',icon:Plain,group:'Connections',badge:2}
const own=[{id:'home',title:'Home'},tunnel,{id:'vpn',title:'VPN',group:'Connections'},{id:'scan',title:'Scanning',hidden:true}]

test("the App's own pages fill the sidebar in order, grouped, with icons and badges",async()=>{
 const shell=await mount({modelValue:'home',pages:own})
 expect(seen.groups).toEqual([
  {items:[{key:'home',label:'Home',icon:undefined,badge:undefined}]},
  {label:'Connections',items:[{key:'tunnel',label:'Tunnel',icon:Plain,badge:2},{key:'vpn',label:'VPN',icon:undefined,badge:undefined}]},
 ])
 shell.unmount()
})

test("a page placed at the bottom is the sidebar's last entry, in a group of its own",async()=>{
 const settings={id:'settings',title:'Settings',placement:'bottom' as const}
 const shell=await mount({modelValue:'home',pages:[settings,...own]})
 expect(seen.groups).toEqual([
  {items:[{key:'home',label:'Home',icon:undefined,badge:undefined}]},
  {label:'Connections',items:[{key:'tunnel',label:'Tunnel',icon:Plain,badge:2},{key:'vpn',label:'VPN',icon:undefined,badge:undefined}]},
  {items:[{key:'settings',label:'Settings',icon:undefined,badge:undefined}]},
 ])
 shell.unmount()
})

test("the current page renders its component under its own title and description",async()=>{
 const pages=[{id:'home',title:'Home',component:Plain,description:'All good'},{id:'scan',title:'Scanning',hidden:true,component:Plain}]
 const shell=await mount({modelValue:'scan',pages,description:'shell-wide'})
 expect(shell.root.querySelector('h1,h2')?.textContent).toBe('Scanning')
 expect(shell.root.querySelectorAll('.page-body')).toHaveLength(1)
 expect(shell.root.textContent).toContain('shell-wide')
 shell.unmount()
 const home=await mount({modelValue:'home',pages})
 expect(home.root.textContent).toContain('All good')
 home.unmount()
})

test('a page that draws its own header gets none from the shell',async()=>{
 const shell=await mount({modelValue:'home',pages:[{id:'home',title:'Home',header:false}]})
 expect(shell.root.querySelector('h1,h2')).toBeNull()
 shell.unmount()
})

// The App owns the current page: the sidebar asks for a page through v-model.
async function bound(props:Record<string,unknown>,start='home'){
 const page=ref(start)
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{locale:'en',pages:own,...props,modelValue:page.value,'onUpdate:modelValue':(value:string)=>{page.value=value}})})
 app.mount(root);await nextTick()
 const scroller=root.querySelector<HTMLElement>('.app-page')!
 let top=0;Object.defineProperty(scroller,'scrollTop',{get:()=>top,set:value=>{top=value}})
 return {page,scroller,async go(key:string){top=120;root.querySelector<HTMLElement>(`[data-key="${key}"]`)!.click();await nextTick();await nextTick();return top},unmount(){app.unmount();root.remove()}}
}

test("moving to another of the App's pages updates v-model and starts the page at the top",async()=>{
 const shell=await bound({})
 expect(await shell.go('vpn')).toBe(0)
 expect(shell.page.value).toBe('vpn')
 shell.unmount()
})

test('an App may keep the scroll position across pages',async()=>{
 const shell=await bound({keepScroll:true})
 expect(await shell.go('vpn')).toBe(120)
 shell.unmount()
})

test('the built-in pages keep the scroll position as before',async()=>{
 const shell=await bound({pages:['status','background']},'status')
 expect(await shell.go('background')).toBe(120)
 expect(shell.page.value).toBe('background')
 shell.unmount()
})

test("the page's actions, the toolbar, the notices and the body each know the current page",async()=>{
 const slot=(name:string)=>({page}:{page:string})=>h('i',{class:name},`${name}:${page}`)
 const shell=await mount({modelValue:'vpn',pages:own},{actions:slot('actions'),toolbar:slot('toolbar'),notices:slot('notices'),default:slot('body')})
 const text=(selector:string)=>shell.root.querySelector(selector)?.textContent
 expect(text('header .toolbar')).toBe('toolbar:vpn')
 expect(text('.actions')).toBe('actions:vpn')
 expect(text('.body')).toBe('body:vpn')
 expect(text('.notices')).toBe('notices:vpn')
 shell.unmount()
})

// Notices sit where most Apps had them: in the page, under its header, inside
// the page's own padding, and they scroll with it.
test('the notices sit in the page under its header and above the body',async()=>{
 const notice=()=>h('i',{class:'notices'},'notice')
 const shell=await mount({modelValue:'vpn',pages:own},{notices:notice,default:()=>h('i',{class:'body'})})
 const page=shell.root.querySelector('.app-page')!
 const notices=page.querySelector(':scope > .app-notices')
 expect(notices?.textContent).toBe('notice')
 expect(notices?.previousElementSibling?.querySelector('h1,h2')?.textContent).toBe('VPN')
 expect(notices?.nextElementSibling?.querySelector('.body')).not.toBeNull()
 shell.unmount()
 const headless=await mount({modelValue:'home',pages:[{id:'home',title:'Home',header:false}]},{notices:notice})
 expect(headless.root.querySelector('.app-page > .app-notices:first-child')).not.toBeNull()
 headless.unmount()
})

test('a shell without a toolbar or notices adds neither',async()=>{
 const shell=await mount({})
 expect(shell.root.querySelector('header')).toBeNull()
 expect(shell.root.querySelector('.app-notices')).toBeNull()
 shell.unmount()
})

// A template whose notices are all v-if false still passes the slot: it
// renders only comments and whitespace, and must not leave the notices' gap.
test('notices that render nothing add no notices block',async()=>{
 const empty=()=>[createCommentVNode('v-if',true),createTextVNode('  '),h(Fragment,[createCommentVNode('v-if',true),h(Fragment,[createTextVNode('\n')])])]
 const shell=await mount({modelValue:'vpn',pages:own},{notices:empty})
 expect(shell.root.querySelector('.app-notices')).toBeNull()
 shell.unmount()
 const nested=await mount({modelValue:'vpn',pages:own},{notices:()=>[createCommentVNode('v-if',true),h(Fragment,[h('i',{class:'notice'},'notice')])]})
 expect(nested.root.querySelector('.app-notices .notice')?.textContent).toBe('notice')
 nested.unmount()
 const text=await mount({modelValue:'vpn',pages:own},{notices:()=>[createTextVNode('Saved')]})
 expect(text.root.querySelector('.app-notices')?.textContent).toBe('Saved')
 text.unmount()
})

test('the page takes the width the App asks for',async()=>{
 const shell=await mount({pageWidth:'fill'})
 expect(seen.width).toBe('fill')
 shell.unmount()
 await mount({}).then(other=>{expect(seen.width).toBeUndefined();other.unmount()})
})

test("a full page says whether the signed-in account is an administrator, in the App's locale",async()=>{
 const admin=await mount({ctx:ctx({fullPage:true,user:{name:'root',isAdmin:true}})})
 expect(admin.root.querySelector('footer')?.textContent).toMatch(/root\s*Administrator$/)
 admin.unmount()
 const user=await mount({locale:'zh-TW',ctx:ctx({fullPage:true,user:{name:'alice',isAdmin:false}})})
 expect(user.root.querySelector('footer')?.textContent).toMatch(/alice\s*DSM 使用者$/)
 user.unmount()
})

// A window context that is not the whole SDK Context still lays out a full page.
test('the shell reads only the launch, the brand and the account from its context',async()=>{
 const own:ShellContext={fullPage:true,displayName:'Reclaim',appIcon:'icon.png',user:{name:'alice',isAdmin:false}}
 const byHost:ShellContext={host:{platform:()=>({web:true,host:'standalone-web',dsmStandalone:null,dsmLanguage:null,appLocale:null,systemVersion:null,fullVersion:null,theme:null})}}
 // @ts-expect-error Without fullPage the shell needs the host to tell the launch.
 const neither:ShellContext={displayName:'Reclaim'}
 void neither
 void h(AppShell,{locale:'en',modelValue:'status',pages:['status'],ctx:own})
 const shell=await mount({ctx:own})
 expect(seen.layout).toMatchObject({wideLayout:'web',brandName:'Reclaim',brandIcon:'icon.png'})
 shell.unmount()
 const hosted=await mount({ctx:byHost})
 expect(seen.layout).toMatchObject({wideLayout:'web'})
 hosted.unmount()
})

// An account the App refuses sees one page that says so, without the App's pages.
test('a denied shell shows only a page that says the account has no access',async()=>{
 const shell=await mount({denied:true,modelValue:'vpn',pages:own},{default:()=>h('i',{class:'body'}),notices:()=>h('i',{class:'notices'})})
 expect(shell.root.querySelector('nav')).toBeNull()
 expect(shell.root.querySelector('.body,.notices')).toBeNull()
 expect(shell.root.querySelector('h1,h2')?.textContent).toBe('No access')
 expect(shell.root.querySelector('[role="alert"]')?.textContent).toMatch(/Application Privileges/)
 shell.unmount()
 const zh=await mount({denied:true,locale:'zh-TW'})
 expect(zh.root.querySelector('h1,h2')?.textContent).toBe('無法存取')
 zh.unmount()
})

test('a denied page takes the title and message the App gives',async()=>{
 const shell=await mount({denied:{title:'Gopeed',message:'Ask for Gopeed.'},pageWidth:'fill'})
 expect(shell.root.querySelector('h1,h2')?.textContent).toBe('Gopeed')
 expect(shell.root.querySelector('[role="alert"]')?.textContent).toBe('Ask for Gopeed.')
 expect(seen.width).toBe('fill')
 shell.unmount()
})

test("the denied slot replaces the message; header: false drops the shell's header",async()=>{
 const shell=await mount({denied:true},{denied:()=>h('form',{class:'sign-in'})})
 expect(shell.root.querySelector('h1,h2')?.textContent).toBe('No access')
 expect(shell.root.querySelector('.sign-in')).not.toBeNull()
 expect(shell.root.querySelector('[role="alert"]')).toBeNull()
 shell.unmount()
 const own=await mount({denied:{header:false}},{denied:()=>h('form',{class:'sign-in'})})
 expect(own.root.querySelector('h1,h2')).toBeNull()
 expect(own.root.querySelector('.sign-in')).not.toBeNull()
 own.unmount()
})

test('a shell that is not denied lays out its pages',async()=>{
 const shell=await mount({denied:false})
 expect(shell.root.querySelector('nav')).not.toBeNull()
 expect(shell.root.querySelector('[role="alert"]')).toBeNull()
 shell.unmount()
})

// AppRoot, opted into: dialogs may leave a DSM window, and a press on one brings the window to the front.
test('app-root wraps a DSM window in an app-local AppRoot whose dialogs leave the window',async()=>{
 const activateWindow=vi.fn(()=>({status:'requested' as const}))
 const window=ctx(brand);Object.assign(window.host,{activateWindow})
 const shell=await mount({appRoot:true,ctx:window})
 expect(seen.root).toMatchObject({modalScope:'app',dialogBounds:'viewport',style:'height: 100%;'})
 expect(shell.root.querySelector('[data-app-root] section')).not.toBeNull()
 seen.activate!()
 expect(activateWindow).toHaveBeenCalledOnce()
 shell.unmount()
})

test('app-root keeps the dialogs of a full page, a shell without ctx and a denied page in the App',async()=>{
 const page=await mount({appRoot:true,ctx:ctx({...brand,fullPage:true})})
 expect(seen.root).toMatchObject({modalScope:'app',dialogBounds:'app'})
 seen.activate!()
 page.unmount()
 const bare=await mount({appRoot:true})
 expect(seen.root).toMatchObject({dialogBounds:'app'})
 seen.activate!()
 bare.unmount()
 const denied=await mount({appRoot:true,denied:true})
 expect(denied.root.querySelector('[data-app-root] [role="alert"]')).not.toBeNull()
 denied.unmount()
})

test('a shell without app-root adds no AppRoot',async()=>{
 const shell=await mount({ctx:ctx(brand)})
 expect(seen.root).toBeUndefined()
 shell.unmount()
})

// The App's root used to hold the UI's locale; app-root holds it now.
test("app-root gives the UI components the App's locale",async()=>{
 const shell=await mount({appRoot:true,locale:'cht'})
 expect(seen.config).toBe('zh-TW')
 expect(shell.root.querySelector('[data-app-root] > [data-config] > section')).not.toBeNull()
 shell.unmount()
 seen.config=undefined
 const bare=await mount({locale:'cht'})
 expect(seen.config).toBeUndefined()
 bare.unmount()
})

// A treemap, a table paged by height or a log viewer fills the page under its
// header and scrolls itself; the page does not scroll.
test('a fill page fills the height under its header and does not scroll itself',async()=>{
 const pages=[{id:'map',title:'Map',fill:true,component:Plain},{id:'home',title:'Home'}]
 const shell=await mount({modelValue:'map',pages})
 expect(shell.root.querySelector('.app-page')?.classList.contains('app-page-fill')).toBe(true)
 expect(shell.root.querySelector('section')?.classList.contains('app-shell-fill')).toBe(true)
 shell.unmount()
 const home=await mount({modelValue:'home',pages})
 expect(home.root.querySelector('.app-page-fill,.app-shell-fill')).toBeNull()
 home.unmount()
})

// (Reclaim): a notice component that renders nothing still passed the
// slot a vnode, and the empty notices block left a 16px gap under the header.
// The block shows by what is rendered in it, also when that changes later.
test('a notice component that renders nothing leaves no notices block',async()=>{
 const shown=ref(false)
 const Banner=defineComponent({setup:()=>()=>shown.value?h('p',{class:'notice'},'Low space'):null})
 const shell=await mount({modelValue:'vpn',pages:own},{notices:()=>[h(Banner)]})
 const visible=()=>shell.root.querySelector('.app-notices:not([hidden])')
 expect(visible()).toBeNull()
 shown.value=true;await nextTick();await nextTick()
 expect(visible()?.textContent).toBe('Low space')
 shown.value=false;await nextTick();await nextTick()
 expect(visible()).toBeNull()
 shell.unmount()
})

test('entry refusal codes show the common remedy and honor display names and explicit copy',async()=>{
 for(const code of ['app_privilege_denied','admin_required']){
  const shell=await mount({denied:{code},locale:'zh-TW',ctx:ctx({displayName:'下載中心'})})
  expect(shell.root.querySelector('[role="alert"]')?.textContent).toBe(appRefusal({code},'zh-TW','下載中心')!.message)
  shell.unmount()
 }
 const unnamed=await mount({denied:{code:'app_privilege_denied'}})
 expect(unnamed.root.textContent).toContain('select this app and click Edit');unnamed.unmount()
 const named=await mount({denied:{code:'app_privilege_denied'},ctx:ctx({displayName:'Old'}),brandName:'New'})
 expect(named.root.textContent).toContain('select New and click Edit');named.unmount()
 const own=await mount({denied:{code:'admin_required',title:'Mine',message:'Own message'}})
 expect(own.root.textContent).toContain('Own message');own.unmount()
})

// Both branches own a PageContent; fallthrough attributes on AppLayout cannot
// choose its surface. Keep the installed UI's default when the prop is absent.
test.each([false,true])('page surfaces are forwarded to PageContent (denied=%s)',async denied=>{
 for(const surface of [undefined,'auto','flat','card','none'] as const){
  const shell=await mount({denied,pageSurface:surface,pageWidth:'readable'})
  expect(seen.surface).toBe(surface)
  expect(seen.width).toBe('readable')
  shell.unmount()
 }
})

test('changing the surface and entering or leaving denial keeps the current surface',async()=>{
 const surface=ref<AppShellPageSurface|undefined>('card')
 const denied=ref(false)
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppShell,{locale:'en',modelValue:'status',pages:['status'],pageSurface:surface.value,denied:denied.value})})
 app.mount(root);await nextTick()
 try{
  expect(seen.surface).toBe('card')
  surface.value='none';await nextTick()
  expect(seen.surface).toBe('none')
  denied.value=true;await nextTick()
  expect(root.querySelector('.app-denied')).not.toBeNull()
  expect(seen.surface).toBe('none')
  surface.value='flat';await nextTick()
  expect(seen.surface).toBe('flat')
  denied.value=false;await nextTick()
  expect(root.querySelector('.app-page')).not.toBeNull()
  expect(seen.surface).toBe('flat')
  surface.value=undefined;await nextTick()
  expect(seen.surface).toBeUndefined()
 }finally{app.unmount();root.remove()}
})

test('the public surface prop includes none while rejecting unknown surfaces',()=>{
 expectTypeOf<InstanceType<typeof AppShell>['$props']['pageSurface']>().toEqualTypeOf<AppShellPageSurface|undefined>()
 expectTypeOf<AppShellPageSurface>().toEqualTypeOf<'auto'|'flat'|'card'|'none'>()
 // @ts-expect-error A surface is a known PageContent mode, not arbitrary CSS.
 const invalid:AppShellPageSurface='rounded'
 void invalid
})
