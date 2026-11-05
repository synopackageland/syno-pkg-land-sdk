// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {fitPortalHeader,portalAccountAttr,portalMobileAttr,readPortalChrome} from './portal-chrome'
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
