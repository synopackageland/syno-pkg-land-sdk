// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {fitPortalHeader,portalAccountAttr,portalMobileAttr,portalNameAttr,portalNameRoom,readPortalChrome,watchPortalName} from './portal-chrome'
import {portalFixture} from './portal-chrome.test-fixture'
afterEach(()=>{document.body.replaceChildren();vi.restoreAllMocks()})
function fixture(){
 const f=portalFixture(),app=document.createElement('section');f.main.append(app)
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockReturnValue({width:1200,height:39} as DOMRect)
 return {...f,app}
}
test('reads the visible portal title and account independently',()=>{
 const f=fixture()
 expect(readPortalChrome(f.app)).toEqual({brand:true,user:true})
 f.account.style.display='none'
 expect(readPortalChrome(f.app)).toEqual({brand:true,user:false})
 f.title.style.visibility='hidden'
 expect(readPortalChrome(f.app)).toEqual({brand:false,user:false})
})
test('no-banner marker, zero-size or hidden ancestors keep the shell chrome',()=>{
 const f=fixture()
 f.main.classList.add('sds-standalone-main-window--no-banner')
 expect(readPortalChrome(f.app)).toEqual({brand:false,user:false})
 f.main.classList.remove('sds-standalone-main-window--no-banner');f.main.style.display='none'
 expect(readPortalChrome(f.app)).toEqual({brand:false,user:false})
 f.main.style.display='';vi.mocked(HTMLElement.prototype.getBoundingClientRect).mockReturnValue({width:0,height:0} as DOMRect)
 expect(readPortalChrome(f.app)).toEqual({brand:false,user:false})
})
test('desktop windows, detached roots and other apps do not borrow a portal header',()=>{
 const f=fixture(),other=document.createElement('div');document.body.append(other)
 expect(readPortalChrome(other)).toEqual({brand:false,user:false})
 expect(readPortalChrome(document.createElement('div'))).toEqual({brand:false,user:false})
 f.main.className='x-window'
 expect(readPortalChrome(f.app)).toEqual({brand:false,user:false})
})
test('unreadable DOM fails open',()=>{
 const f=fixture();vi.spyOn(f.app,'closest').mockImplementation(()=>{throw new Error('unavailable')})
 expect(readPortalChrome(f.app)).toEqual({brand:false,user:false})
})

// In the UI's mobile layout DSM's header keeps only the App icon and an account
// icon; the stylesheet keys on these marks, desktop widths get none.
test('the mobile layout marks the portal header and names the account in its menu',()=>{
 const f=fixture(),menu=document.createElement('div')
 menu.className='x-menu sds-standalone-main-window-header__toolbar__menu';document.body.append(menu)
 const undo=fitPortalHeader(f.app,true)
 expect(f.main.hasAttribute(portalMobileAttr)).toBe(true)
 expect(menu.getAttribute(portalAccountAttr)).toBe('alice')
 undo()
 expect(f.main.hasAttribute(portalMobileAttr)).toBe(false)
 expect(menu.hasAttribute(portalAccountAttr)).toBe(false)
 fitPortalHeader(f.app,false)
 expect(f.main.hasAttribute(portalMobileAttr)).toBe(false)
 expect(menu.hasAttribute(portalAccountAttr)).toBe(false)
})
test('a header without the account button still hides the name; no banner and other windows stay unmarked',()=>{
 const f=fixture(),menu=document.createElement('div')
 menu.className='sds-standalone-main-window-header__toolbar__menu';document.body.append(menu)
 f.account.remove()
 fitPortalHeader(f.app,true)
 expect(f.main.hasAttribute(portalMobileAttr)).toBe(true)
 expect(menu.hasAttribute(portalAccountAttr)).toBe(false)
 f.main.classList.add('sds-standalone-main-window--no-banner')
 fitPortalHeader(f.app,true)
 expect(f.main.hasAttribute(portalMobileAttr)).toBe(false)
 const other=document.createElement('div');document.body.append(other)
 fitPortalHeader(other,true)
 expect(document.querySelector(`[${portalMobileAttr}]`)).toBe(null)
})

// Desktop and tablet widths: a header laid out as DSM does, the name from x=0
// with a 34px icon padding, its text ending at `text`, the account button at
// `button` (both on the name's line unless moved). Frames and resizes are
// driven by hand.
function nameFixture(){
 const f=fixture(),name=f.title.querySelector<HTMLElement>('.x-window-header-text')!
 name.style.paddingLeft='34px'
 const geo={text:400,button:600,buttonWidth:200,buttonTop:10}
 const rect=(left:number,width:number,top=10,height=20)=>({left,right:left+width,top,bottom:top+height,width,height,x:left,y:top,toJSON(){}}) as DOMRect
 vi.restoreAllMocks()
 vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockImplementation(function(this:HTMLElement){
  if(this===f.account) return rect(geo.button,geo.buttonWidth,geo.buttonTop)
  if(this===name) return rect(0,geo.text)
  return rect(0,1200,0,39)
 })
 const proto=Range.prototype as unknown as Record<string,unknown>,saved={b:proto.getBoundingClientRect,c:proto.getClientRects}
 proto.getBoundingClientRect=()=>rect(34,geo.text-34)
 proto.getClientRects=()=>[rect(34,geo.text-34)]
 const frames:FrameRequestCallback[]=[],observed=new Set<Element>()
 vi.spyOn(window,'requestAnimationFrame').mockImplementation(callback=>frames.push(callback))
 vi.spyOn(window,'cancelAnimationFrame').mockImplementation(id=>{frames[id-1]=()=>{}})
 let onResize=()=>{}
 const win=window as unknown as {ResizeObserver?:unknown},savedRO=win.ResizeObserver
 win.ResizeObserver=class{constructor(callback:()=>void){onResize=callback} observe(e:Element){observed.add(e)} unobserve(e:Element){observed.delete(e)} disconnect(){observed.clear()}}
 const flush=async()=>{await Promise.resolve();const due=frames.splice(0);due.forEach(callback=>callback(0))}
 restore.push(()=>{proto.getBoundingClientRect=saved.b;proto.getClientRects=saved.c;win.ResizeObserver=savedRO})
 return {...f,name,geo,frames,observed,flush,resize:()=>onResize()}
}
const restore:Array<()=>void>=[]
afterEach(()=>{restore.splice(0).forEach(undo=>undo())})
test('a name that fits is left as DSM draws it',()=>{
 const f=nameFixture()
 const stop=watchPortalName(f.app)
 expect(f.main.hasAttribute(portalNameAttr)).toBe(false)
 expect(f.main.getAttribute('style')).toBe(null)
 expect(f.name.hasAttribute('title')).toBe(false)
 expect(f.observed.has(f.main) && f.observed.has(f.account)).toBe(true)
 stop()
 expect(f.observed.size).toBe(0)
})
test('a name that would run under the account button ends 12px before it, with its tooltip',async()=>{
 const f=nameFixture();f.geo.text=700
 const stop=watchPortalName(f.app)
 expect(f.main.getAttribute(portalNameAttr)).toBe('cut')
 expect(f.main.style.getPropertyValue(portalNameRoom)).toBe('588px')
 expect(f.name.getAttribute('title')).toBe('Portal App')
 expect(f.name.textContent).toBe('Portal App')
 // A wider account: one measurement per frame, however many changes.
 f.geo.button=500;f.resize();f.resize();f.account.querySelector('button')!.textContent='alice.long'
 await Promise.resolve()
 expect(f.frames.length).toBe(1)
 await f.flush()
 expect(f.main.style.getPropertyValue(portalNameRoom)).toBe('488px')
 // Less than an icon's width of text: the name draws no text, keeps its tooltip.
 f.geo.button=60;f.resize();await f.flush()
 expect(f.main.getAttribute(portalNameAttr)).toBe('icon')
 expect(f.name.getAttribute('title')).toBe('Portal App')
 // The button not on the name's line, then hidden: nothing to avoid.
 f.geo.buttonTop=60;f.resize();await f.flush()
 expect(f.main.hasAttribute(portalNameAttr)).toBe(false)
 expect(f.name.hasAttribute('title')).toBe(false)
 f.geo.buttonTop=10;f.resize();await f.flush()
 expect(f.main.getAttribute(portalNameAttr)).toBe('icon')
 f.geo.buttonWidth=0;f.resize();await f.flush()
 expect(f.main.hasAttribute(portalNameAttr)).toBe(false)
 expect(f.main.style.getPropertyValue(portalNameRoom)).toBe('')
 f.geo.buttonWidth=200;f.geo.button=500;f.resize();await f.flush()
 expect(f.main.getAttribute(portalNameAttr)).toBe('cut')
 stop()
 expect(f.main.hasAttribute(portalNameAttr)).toBe(false)
 expect(f.main.style.getPropertyValue(portalNameRoom)).toBe('')
 expect(f.name.hasAttribute('title')).toBe(false)
 // Stopped: later changes are not watched.
 f.resize();await f.flush()
 expect(f.main.hasAttribute(portalNameAttr)).toBe(false)
})
test('the fit stays put once applied: its own marks schedule no further change',async()=>{
 const f=nameFixture();f.geo.text=700
 const stop=watchPortalName(f.app)
 await f.flush();await f.flush()
 expect(f.frames.length).toBe(0)
 expect(f.main.getAttribute(portalNameAttr)).toBe('cut')
 stop()
})
test('the mobile layout, a missing account button and DSM\'s own tooltip are respected',async()=>{
 const f=nameFixture();f.geo.text=700
 f.name.setAttribute('title','DSM title')
 const stop=watchPortalName(f.app)
 expect(f.main.getAttribute(portalNameAttr)).toBe('cut')
 expect(f.name.getAttribute('title')).toBe('DSM title')
 fitPortalHeader(f.app,true);await f.flush()
 expect(f.main.hasAttribute(portalNameAttr)).toBe(false)
 fitPortalHeader(f.app,false);await f.flush()
 expect(f.main.getAttribute(portalNameAttr)).toBe('cut')
 // DSM fills in the account late: removed, then added again.
 const toolbar=f.account.parentElement!;f.account.remove();await f.flush()
 expect(f.main.hasAttribute(portalNameAttr)).toBe(false)
 toolbar.append(f.account);await f.flush()
 expect(f.main.getAttribute(portalNameAttr)).toBe('cut')
 expect(f.observed.has(f.account)).toBe(true)
 stop()
 expect(f.name.getAttribute('title')).toBe('DSM title')
})
