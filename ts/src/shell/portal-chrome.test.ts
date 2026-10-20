// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {readPortalChrome} from './portal-chrome'
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
